package controller

import "testing"

func TestCapabilityAnswerMatches(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"exact final answer", "reasoning\nFINAL_ANSWER: 21", true},
		{"trailing whitespace", "FINAL_ANSWER: 21\n", true},
		{"wrong answer", "FINAL_ANSWER: 20", false},
		{"answer mentioned earlier only", "The answer is 21, but I cannot conclude", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capabilityAnswerMatches(tt.text); got != tt.want {
				t.Fatalf("capabilityAnswerMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}
