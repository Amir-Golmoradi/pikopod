package sanitize

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pikopod/pikopod/internal/pathtmpl"
)

func TestTokenKeepsADigitWhenTheValueHadOne(t *testing.T) {
	for i := 0; i < 2000; i++ {
		tok := NewTokenizer(fmt.Sprintf("key-%d", i), "s", 1)
		prefixed, _ := tok.Tokenize("ch_CANARYQ7Z9")
		if pathtmpl.ClassifySegment(prefixed) != "ch_{id}" {
			t.Fatalf("key %d: a prefixed id must still read as one after tokenizing: %q", i, prefixed)
		}
		if !strings.HasPrefix(prefixed, "ch_") || len(prefixed) != len("ch_CANARYQ7Z9") {
			t.Fatalf("key %d: format must be preserved: %q", i, prefixed)
		}
		alnum, _ := tok.Tokenize("ABCDEF12")
		if pathtmpl.ClassifySegment(alnum) != "{id}" {
			t.Fatalf("key %d: an alphanumeric id must still read as one after tokenizing: %q", i, alnum)
		}
	}
}
