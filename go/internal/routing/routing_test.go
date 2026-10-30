// Tests for the routing file: parsing, startup validation, and the fairness rule for single-FI and shared queues.
package routing

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const twoQueues = `
coreQueues:
  bulkwire-core-lakeshore:
    fis: [lakeshore]
    activitiesPerSecond: 10
    maxConcurrentActivities: 8
  bulkwire-core-prairie:
    fis: [prairie]
    activitiesPerSecond: 10
    maxConcurrentActivities: 8
fiDefaults:
  lakeshore: { chunkConcurrency: 4 }
`

const sharedCluster = `
coreQueues:
  bulkwire-core-shared-cluster-1:
    fis: [fiA, fiB]
    activitiesPerSecond: 5
    maxConcurrentActivities: 4
`

func TestRepositoryRoutingFileIsValid(t *testing.T) {
	c, err := Load("../../../config/routing.yaml")
	require.NoError(t, err)
	require.NoError(t, c.Validate([]string{"lakeshore", "prairie"}))
	q, ok := c.QueueFor("lakeshore")
	require.True(t, ok)
	require.Equal(t, "bulkwire-core-lakeshore", q.Name)
	require.Equal(t, 4, c.ChunkConcurrency("prairie", 99))
}

func TestSingleFIQueueIsFairByBatch(t *testing.T) {
	c, err := Parse([]byte(twoQueues))
	require.NoError(t, err)
	require.NoError(t, c.Validate([]string{"lakeshore", "prairie"}))

	r, err := c.Resolve("lakeshore", "01BATCH")
	require.NoError(t, err)
	require.Equal(t, Route{CoreTaskQueue: "bulkwire-core-lakeshore", CoreFairnessKey: "01BATCH"}, r)
	r, err = c.Resolve("prairie", "01OTHER")
	require.NoError(t, err)
	require.Equal(t, Route{CoreTaskQueue: "bulkwire-core-prairie", CoreFairnessKey: "01OTHER"}, r)

	_, err = c.Resolve("unknown", "01BATCH")
	require.Error(t, err)
	require.Equal(t, 4, c.ChunkConcurrency("lakeshore", 2))
	require.Equal(t, 2, c.ChunkConcurrency("prairie", 2), "an FI without fiDefaults uses the default")
}

func TestSharedQueueIsFairByFIAndItsWorkerHoldsBothFIs(t *testing.T) {
	c, err := Parse([]byte(sharedCluster))
	require.NoError(t, err)
	require.NoError(t, c.Validate([]string{"fiA", "fiB"}))

	for _, fi := range []string{"fiA", "fiB"} {
		r, err := c.Resolve(fi, "01BATCH")
		require.NoError(t, err)
		require.Equal(t, Route{CoreTaskQueue: "bulkwire-core-shared-cluster-1", CoreFairnessKey: fi}, r)
	}

	// The queue's worker is built with Core credentials for exactly the FIs it serves (cmd/worker does this).
	endpoints := map[string]string{"fiA": "http://core-a", "fiB": "http://core-b", "fiC": "http://core-c"}
	q, _ := c.QueueFor("fiA")
	require.Equal(t, map[string]string{"fiA": "http://core-a", "fiB": "http://core-b"}, Subset(endpoints, q.FIs))
}

func TestValidateFailsFastWithEveryProblem(t *testing.T) {
	c, err := Parse([]byte(`
coreQueues:
  queue-a:
    fis: [lakeshore, prairie]
    activitiesPerSecond: 0
    maxConcurrentActivities: 8
  queue-b:
    fis: [prairie, ghost]
    activitiesPerSecond: 1
    maxConcurrentActivities: -1
  queue-c:
    fis: []
    activitiesPerSecond: 1
    maxConcurrentActivities: 1
fiDefaults:
  lakeshore: { chunkConcurrency: 0 }
`))
	require.NoError(t, err)
	err = c.Validate([]string{"lakeshore", "prairie", "orphan"})
	require.Error(t, err)
	for _, want := range []string{
		`queue "queue-a": activitiesPerSecond must be positive`,
		`queue "queue-b": maxConcurrentActivities must be positive`,
		`queue "queue-c" serves no FIs`,
		`FI "prairie" is in two queues`,
		`lists FI "ghost", which has no Core endpoint`,
		`FI "orphan" is in no Core queue`,
		`fiDefaults.lakeshore.chunkConcurrency must be 1..8`,
	} {
		require.True(t, strings.Contains(err.Error(), want), "missing %q in:\n%s", want, err)
	}

	empty, err := Parse([]byte("coreQueues: {}\n"))
	require.NoError(t, err)
	require.ErrorContains(t, empty.Validate([]string{"lakeshore"}), "coreQueues is empty")
}

func TestLoadReportsAMissingFile(t *testing.T) {
	_, err := Load(os.DevNull + "/missing.yaml")
	require.Error(t, err)
}
