// Tests that the Workflow ID is the business identifier bulkwire/{fiId}/{batchId} and that the Search
// Attribute keys are exactly the five required ones.

package temporalx

import (
	"strings"
	"testing"
)

func TestWorkflowIDIsTheBusinessIdentifier(t *testing.T) {
	if got := WorkflowID("lakeshore", "01JBATCH"); got != "bulkwire/lakeshore/01JBATCH" {
		t.Fatalf("got %s", got)
	}
}

func TestSearchAttributeKeysMatchTheRequiredSet(t *testing.T) {
	for _, name := range []string{SABulkWireStatus.GetName(), SAFiID.GetName(), SACustomerID.GetName(),
		SAInitiatedBy.GetName(), SANeedsAttention.GetName()} {
		if _, ok := RequiredSearchAttributes[name]; !ok {
			t.Errorf("%s is not in RequiredSearchAttributes", name)
		}
	}
	if RequiredSearchAttributes["NeedsAttention"] != "Bool" || !strings.EqualFold(RequiredSearchAttributes["FiId"], "keyword") {
		t.Fatal("wrong types")
	}
}
