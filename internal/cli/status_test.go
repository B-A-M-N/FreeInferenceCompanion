package cli

import (
	"bufio"
	"strings"
	"testing"
)

func TestStdinHasDataPeeksThroughBufferedReader(t *testing.T) {
	if !stdinHasData(bufio.NewReader(strings.NewReader(`{"session_id":"s1"}`))) {
		t.Fatal("buffered reader with underlying data was treated as empty")
	}
	if stdinHasData(bufio.NewReader(strings.NewReader(""))) {
		t.Fatal("empty buffered reader was treated as status-line input")
	}
}
