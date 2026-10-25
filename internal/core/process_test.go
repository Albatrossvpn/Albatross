package core

import "testing"

func TestMergeEnv(t *testing.T) {
	got := mergeEnv([]string{"A=1", "B=2", "NOEQ"}, []string{"B=3", "C=4", "NOEQ"})
	want := []string{"A=1", "B=3", "NOEQ", "C=4"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
