package main

import "testing"

func TestStalled(t *testing.T) {
	recv := int64(1000)
	tests := []struct {
		name string
		sent int64
		now  int64
		want bool
	}{
		{"idle, no send after last reply", 1000, 5000, false},
		{"reply but nothing sent since", 1000, 9000, false},
		{"sending, within grace", 1005, 1020, false},
		{"sending, just past grace", 1005, 1026, true},
		{"long dead", 1005, 4000, true},
	}
	for _, tt := range tests {
		if got := stalled(tt.sent, recv, tt.now); got != tt.want {
			t.Errorf("%s: stalled(%d,%d,%d)=%v want %v", tt.name, tt.sent, recv, tt.now, got, tt.want)
		}
	}
}
