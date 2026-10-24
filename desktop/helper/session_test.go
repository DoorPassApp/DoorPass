// Copyright 2026 The DoorPass Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A slow connect (minutes on a first Windows run) must not block status;
// the app treats a status timeout as a missing helper.
func TestStatusAnswersDuringConnect(t *testing.T) {
	m := newManager()
	m.op.Lock() // As connect does for its whole run.
	m.setBusy("connecting")
	defer m.op.Unlock()

	done := make(chan Status, 1)
	go func() { done <- m.status() }()
	select {
	case st := <-done:
		require.Equal(t, "connecting", st.Busy)
		require.False(t, st.Connected)
	case <-time.After(time.Second):
		t.Fatal("status blocked while a connect was running")
	}
}
