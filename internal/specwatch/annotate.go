package specwatch

import "github.com/pikopod/pikopod/internal/drift"

const DocumentedNote = "documented: the provider's new spec version declares this change"

func AnnotateDocumented(findings []drift.Finding, doc *Documented) {
	if doc == nil {
		return
	}
	for i := range findings {
		f := &findings[i]
		documented := false
		switch f.Kind {
		case drift.FieldAdded:
			documented = doc.HasFieldAdded(f.Method, f.Template, f.Field)
		case drift.EnumValueNew:
			documented = doc.HasEnumValueAdded(f.Method, f.Template, f.Field, f.After)
		case drift.StatusNew, drift.StatusCodeChanged:
			documented = doc.HasStatusAdded(f.Method, f.Template, f.After)
		}
		if documented {
			f.Documented = true
			f.Note = DocumentedNote
		}
	}
}
