package api

import (
	"fmt"
	"testing"

	"yzapi/internal/model"
	"yzapi/internal/vector"
)

// R138-01: runtime identity and the shared resolver agree even when the stored URL
// carries trailing slashes.
func TestR138RuntimeIdentityNormalizesURL(t *testing.T) {
	s := auditServer(t)
	enc, _ := s.cipher.Encrypt("k")
	a := model.Account{Name: "r138-vec", Provider: "custom", Type: model.TypeEmbedding, BaseURL: "http://current//", APIKeyEnc: enc, Enabled: true,
		Mappings: []model.ModelMapping{{RequestModel: "embed", UpstreamModel: "provider-embed"}}}
	s.db.Create(&a)
	if err := s.st.SetVector(settingsVector(a.ID, "embed")); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d|http://current|provider-embed", a.ID)
	if got := s.VectorIdentity(); got != want {
		t.Fatalf("runtime identity: %q want %q", got, want)
	}
	if r, err := vector.Identity(s.db, a.ID, "embed"); err != nil || r.Identity != want {
		t.Fatalf("shared resolver: %+v %v", r, err)
	}
}
