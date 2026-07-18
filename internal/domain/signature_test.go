package domain

import "testing"

func TestValidateSignature(t *testing.T) {
	tests := []struct {
		name      string
		signature string
		wantError bool
	}{
		{
			name:      "simple function",
			signature: "func Increment(value int) int",
		},
		{
			name:      "multiple built-in values",
			signature: "func Split(total, count int) ([]int, error)",
		},
		{
			name:      "function body is not a signature",
			signature: "func Increment(value int) int { return value + 1 }",
			wantError: true,
		},
		{
			name:      "method is not a top-level function",
			signature: "func (Counter) Increment(value int) int",
			wantError: true,
		},
		{
			name:      "undefined type",
			signature: "func Solve(value MissingType) int",
			wantError: true,
		},
		{
			name:      "multiple declarations",
			signature: "func One() int\nfunc Two() int",
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSignature(test.signature)
			if (err != nil) != test.wantError {
				t.Fatalf("ValidateSignature(%q) error = %v, wantError %t", test.signature, err, test.wantError)
			}
		})
	}
}
