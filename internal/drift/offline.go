package drift

import "github.com/pikopod/pikopod/internal/baseline"

type OfflineFinding struct {
	Kind   string
	Field  string
	Detail string
}

func DiffRecord(fam *baseline.Family, status int, body any) []OfflineFinding {
	findings := DiffRecordFindings("", fam, status, body)
	out := make([]OfflineFinding, 0, len(findings))
	for _, f := range findings {
		out = append(out, OfflineFinding{Kind: string(f.Kind), Field: f.Field, Detail: f.Detail()})
	}
	return out
}

func DiffRecordFindings(upstream string, fam *baseline.Family, status int, body any) []Finding {
	if fam == nil || !fam.Frozen {
		return nil
	}
	obs := baseline.Observation{Family: fam, Ready: true, Fields: baseline.Flatten(body), Template: fam.Template, Status: status}
	return Diff(upstream, obs, map[string]bool{fam.StatusClass: true})
}

func (f Finding) Detail() string {
	if f.Kind == FieldRemoved {
		return f.Before
	}
	return f.After
}
