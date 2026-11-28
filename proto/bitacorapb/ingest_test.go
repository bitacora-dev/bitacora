package bitacorapb

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestPendingPackageOperationHasOnlyClosedOperationAndRequestMetadata(t *testing.T) {
	desc := (&PendingPackageOperation{}).ProtoReflect().Descriptor()
	if got := desc.Fields().Len(); got != 3 {
		t.Fatalf("expected three fixed fields, got %d", got)
	}
	if field := desc.Fields().ByName("operation"); field == nil || field.Enum() == nil {
		t.Fatal("operation must be a protobuf enum, not free text")
	}
	for _, name := range []string{"request_id", "expires_at_ms"} {
		if desc.Fields().ByName(protoreflect.Name(name)) == nil {
			t.Fatalf("missing required request metadata %q", name)
		}
	}
	if values := desc.Fields().ByName("operation").Enum().Values(); values.Len() != 3 {
		t.Fatalf("expected unspecified plus exactly two operations, got %d values", values.Len())
	}
}
