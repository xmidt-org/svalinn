package main

import (
	"testing"
)

func TestSvalinn(t *testing.T) {
	code := svalinn([]string{"-v"})
	t.Logf("Recieved Code %d", code)
	if code != 0 {
		t.Error("-v should result in a 0 error code")
	}
}
