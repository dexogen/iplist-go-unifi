package iplist

import (
	"strings"
	"testing"
)

func TestNormalizeCIDRValues(t *testing.T) {
	values, err := NormalizeLines("8.8.8.8\n8.8.8.0/24\n8.8.8.1/24\n# comment\n", "ipv4_cidr")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(values, ",")
	want := "8.8.8.0/24,8.8.8.8/32"
	if got != want {
		t.Fatalf("values = %s, want %s", got, want)
	}
}

func TestNormalizeRejectsWrongFamily(t *testing.T) {
	if _, err := NormalizeLines("2001:db8::/32\n", "ipv4_cidr"); err == nil {
		t.Fatal("expected IPv4 family validation error")
	}
}

func TestHashValuesIsStable(t *testing.T) {
	first := HashValues([]string{"a", "b"})
	second := HashValues([]string{"a", "b"})
	if first == "" || first != second {
		t.Fatalf("unstable hash: %q %q", first, second)
	}
}
