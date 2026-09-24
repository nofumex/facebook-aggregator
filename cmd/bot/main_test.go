package main

import "testing"

func TestPoolPartitionReservesUIConnections(t *testing.T) {
	tests := []struct {
		total, requested int
		ui, background   int
	}{
		{total: 5, requested: 2, ui: 3, background: 2},
		{total: 5, requested: 20, ui: 1, background: 4},
		{total: 2, requested: 1, ui: 1, background: 1},
		{total: 1, requested: 1, ui: 1, background: 0},
	}
	for _, tc := range tests {
		ui, background := poolPartition(tc.total, tc.requested)
		if ui != tc.ui || background != tc.background || ui+background != max(tc.total, 1) {
			t.Fatalf("total=%d requested=%d got=%d/%d", tc.total, tc.requested, ui, background)
		}
	}
}
