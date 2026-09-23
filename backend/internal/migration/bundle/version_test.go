package bundle

import "testing"

func TestParseSchemaVersionValid(t *testing.T) {
	v, err := ParseSchemaVersion("1.2.3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Major != 1 || v.Minor != 2 || v.Patch != 3 {
		t.Fatalf("bad parse: %+v", v)
	}
}

func TestParseSchemaVersionInvalid(t *testing.T) {
	cases := []string{"", "1", "1.2", "1.2.x", "a.b.c"}
	for _, c := range cases {
		if _, err := ParseSchemaVersion(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestRejectIfIncompatibleSameMajor(t *testing.T) {
	for _, v := range []string{SchemaVersion, "2.99.0"} {
		if err := RejectIfIncompatible(v); err != nil {
			t.Errorf("expected %q compatible: %v", v, err)
		}
	}
}

func TestRejectIfIncompatibleDifferentMajor(t *testing.T) {
	for _, v := range []string{"0.9.0", "1.2.0", "3.0.0", "10.0.0"} {
		if err := RejectIfIncompatible(v); err == nil {
			t.Errorf("expected %q rejected", v)
		}
	}
}

func TestRejectIfIncompatibleEmpty(t *testing.T) {
	if err := RejectIfIncompatible(""); err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestParseSchemaVersionRejectsNegative(t *testing.T) {
	cases := []string{"1.-1.0", "-1.0.0", "0.0.-1"}
	for _, c := range cases {
		if _, err := ParseSchemaVersion(c); err == nil {
			t.Errorf("expected error for negative version %q", c)
		}
	}
}
