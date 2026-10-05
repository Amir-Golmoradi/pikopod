package drift

import "github.com/pikopod/pikopod/internal/errfmt"

type Risk string

const (
	RiskHigh   Risk = "high"
	RiskMedium Risk = "medium"
	RiskLow    Risk = "low"
)

func (r Risk) Rank() int {
	switch r {
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	}
	return 0
}

func (r Risk) lower() Risk {
	switch r {
	case RiskHigh:
		return RiskMedium
	default:
		return RiskLow
	}
}

func ParseRisk(s string) (Risk, error) {
	switch Risk(s) {
	case RiskHigh, RiskMedium, RiskLow:
		return Risk(s), nil
	}
	return "", errfmt.New("unknown risk tier "+s, "--fail-on takes high, medium or low", "pass one of them; medium is the default", "docs/exit-codes.md")
}

func (k Kind) Risk() Risk {
	switch k {
	case FieldAdded:
		return RiskLow
	case EnumValueNew:
		return RiskMedium
	default:
		return RiskHigh
	}
}

func (f Finding) Risk() Risk {
	r := f.Kind.Risk()
	if f.Documented {
		return r.lower()
	}
	return r
}
