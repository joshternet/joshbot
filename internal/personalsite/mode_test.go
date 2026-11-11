package personalsite

import "testing"

func TestParseScoringMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    ScoringMode
		wantErr bool
	}{
		{raw: "", want: ScoringModeOff},
		{raw: "off", want: ScoringModeOff},
		{raw: "SHADOW", want: ScoringModeShadow},
		{raw: "order", want: ScoringModeOrder},
		{raw: "enabled", wantErr: true},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.raw, func(t *testing.T) {
			t.Parallel()

			got, err := ParseScoringMode(testCase.raw)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("ParseScoringMode() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseScoringMode() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf(
					"ParseScoringMode() = %q, want %q",
					got,
					testCase.want,
				)
			}
			switch testCase.want {
			case ScoringModeOff:
				if got.Active() {
					t.Fatal("off mode Active() = true")
				}
			case ScoringModeShadow, ScoringModeOrder:
				if !got.Active() {
					t.Fatalf("%s mode Active() = false", got)
				}
			}
			if testCase.want == ScoringModeOrder && !got.OrdersAdmission() {
				t.Fatal("order mode OrdersAdmission() = false")
			}
			if testCase.want != ScoringModeOrder && got.OrdersAdmission() {
				t.Fatalf("%s mode OrdersAdmission() = true", got)
			}
		})
	}

	unknown := ScoringMode("weird")
	if unknown.Active() || unknown.OrdersAdmission() {
		t.Fatal("unknown mode should be inactive")
	}
	if ScoringMode("").Active() {
		t.Fatal("empty mode Active() = true")
	}
}
