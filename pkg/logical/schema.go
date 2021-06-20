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

package logical

import (
	"github.com/pkg/errors"

	apiv1 "github.com/apache/skywalking-banyandb/api/fbs/v1"
)

type fieldSpec struct {
	idx  int
	spec *apiv1.FieldSpec
}

type Schema struct {
	fieldMap map[string]*fieldSpec
}

func (s *Schema) RegisterField(name string, i int, spec *apiv1.FieldSpec) {
	s.fieldMap[name] = &fieldSpec{
		idx:  i,
		spec: spec,
	}
}

func (s *Schema) FieldDefined(name string) bool {
	if _, ok := s.fieldMap[name]; ok {
		return true
	}
	return false
}

func (s *Schema) CreateRef(name string) (*fieldRef, error) {
	if fs, ok := s.fieldMap[name]; ok {
		return NewFieldRef(name, fs), nil
	}
	return nil, errors.Wrap(FieldNotDefinedErr, name)
}

func (s *Schema) Map(refs ...*fieldRef) (Schema, error) {
	if refs == nil || len(refs) == 0 {
		return *s, nil
	}
	newS := Schema{}
	for _, ref := range refs {
		if s.FieldDefined(ref.name) {
			newS.fieldMap[ref.name] = ref.spec
		} else {
			return newS, errors.Wrap(FieldNotDefinedErr, ref.name)
		}
	}
	return newS, nil
}
