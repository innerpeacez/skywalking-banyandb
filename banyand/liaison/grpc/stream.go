// Licensed to Apache Software Foundation (ASF) under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Apache Software Foundation (ASF) licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package grpc

import (
	"context"
	"io"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/apache/skywalking-banyandb/api/common"
	"github.com/apache/skywalking-banyandb/api/data"
	streamv1 "github.com/apache/skywalking-banyandb/api/proto/banyandb/stream/v1"
	"github.com/apache/skywalking-banyandb/banyand/tsdb"
	"github.com/apache/skywalking-banyandb/pkg/accesslog"
	"github.com/apache/skywalking-banyandb/pkg/bus"
	"github.com/apache/skywalking-banyandb/pkg/logger"
	"github.com/apache/skywalking-banyandb/pkg/timestamp"
)

type streamService struct {
	streamv1.UnimplementedStreamServiceServer
	*discoveryService
	sampled            *logger.Logger
	ingestionAccessLog accesslog.Log
}

func (s *streamService) setLogger(log *logger.Logger) {
	s.sampled = log.Sampled(10)
}

func (s *streamService) activeIngestionAccessLog(root string) (err error) {
	if s.ingestionAccessLog, err = accesslog.
		NewFileLog(root, "stream-ingest-%s", 10*time.Minute, s.log); err != nil {
		return err
	}
	return nil
}

func (s *streamService) Write(stream streamv1.StreamService_WriteServer) error {
	reply := func(stream streamv1.StreamService_WriteServer, logger *logger.Logger) {
		if errResp := stream.Send(&streamv1.WriteResponse{}); errResp != nil {
			logger.Err(errResp).Msg("failed to send response")
		}
	}
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		writeEntity, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			s.sampled.Error().Stringer("written", writeEntity).Err(err).Msg("failed to receive message")
			reply(stream, s.sampled)
			continue
		}
		if errTime := timestamp.CheckPb(writeEntity.GetElement().Timestamp); errTime != nil {
			s.sampled.Error().Stringer("written", writeEntity).Err(errTime).Msg("the element time is invalid")
			reply(stream, s.sampled)
			continue
		}
		entity, tagValues, shardID, err := s.navigate(writeEntity.GetMetadata(), writeEntity.GetElement().GetTagFamilies())
		if err != nil {
			s.sampled.Error().Err(err).RawJSON("written", logger.Proto(writeEntity)).Msg("failed to navigate to the write target")
			reply(stream, s.sampled)
			continue
		}
		if s.ingestionAccessLog != nil {
			if errAccessLog := s.ingestionAccessLog.Write(writeEntity); errAccessLog != nil {
				s.sampled.Error().Err(errAccessLog).Msg("failed to write ingestion access log")
			}
		}
		iwr := &streamv1.InternalWriteRequest{
			Request:    writeEntity,
			ShardId:    uint32(shardID),
			SeriesHash: tsdb.HashEntity(entity),
		}
		if s.log.Debug().Enabled() {
			iwr.EntityValues = tagValues.Encode()
		}
		message := bus.NewMessage(bus.MessageID(time.Now().UnixNano()), iwr)
		_, errWritePub := s.pipeline.Publish(data.TopicStreamWrite, message)
		if errWritePub != nil {
			s.sampled.Error().Err(errWritePub).RawJSON("written", logger.Proto(writeEntity)).Msg("failed to send a message")
		}
		reply(stream, s.sampled)
	}
}

var emptyStreamQueryResponse = &streamv1.QueryResponse{Elements: make([]*streamv1.Element, 0)}

func (s *streamService) Query(_ context.Context, req *streamv1.QueryRequest) (*streamv1.QueryResponse, error) {
	timeRange := req.GetTimeRange()
	if timeRange == nil {
		req.TimeRange = timestamp.DefaultTimeRange
	}
	if err := timestamp.CheckTimeRange(req.GetTimeRange()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v is invalid :%s", req.GetTimeRange(), err)
	}
	message := bus.NewMessage(bus.MessageID(time.Now().UnixNano()), req)
	feat, errQuery := s.pipeline.Publish(data.TopicStreamQuery, message)
	if errQuery != nil {
		if errors.Is(errQuery, io.EOF) {
			return emptyStreamQueryResponse, nil
		}
		return nil, errQuery
	}
	msg, errFeat := feat.Get()
	if errFeat != nil {
		return nil, errFeat
	}
	data := msg.Data()
	switch d := data.(type) {
	case []*streamv1.Element:
		return &streamv1.QueryResponse{Elements: d}, nil
	case common.Error:
		return nil, errors.WithMessage(errQueryMsg, d.Msg())
	}
	return nil, nil
}

func (s *streamService) Close() error {
	return s.ingestionAccessLog.Close()
}
