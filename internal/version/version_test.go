package version

import (
	"strings"
	"testing"
)

func TestString_ContainsAllFields(t *testing.T) {
	s := String()
	for _, want := range []string{"oh-my-lan", Version, Commit, BuildTime} {
		if !strings.Contains(s, want) {
			t.Errorf("String()=%q 不包含 %q", s, want)
		}
	}
}
