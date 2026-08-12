package common

import "testing"

func TestJoinRawArray(t *testing.T) {
	tests := []struct {
		name  string
		items [][]byte
		want  string
	}{
		{name: "empty", want: "[]"},
		{name: "one", items: [][]byte{[]byte(`{"a":1}`)}, want: `[{"a":1}]`},
		{name: "many", items: [][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`)}, want: `[{"a":1},{"b":2}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := string(JoinRawArray(test.items)); got != test.want {
				t.Fatalf("JoinRawArray() = %s, want %s", got, test.want)
			}
		})
	}
}
