package registry

import "testing"

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"digisac", true},
		{"my-tool", true},
		{"tool_2", true},
		{"", false},
		{"-flag", false},
		{"--help", false},
		{"Upper", false},
		{"a/b", false},
		{"..", false},
		{"with space", false},
	}
	for _, c := range cases {
		if err := ValidateName(c.name); (err == nil) != c.ok {
			t.Errorf("ValidateName(%q) err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}
