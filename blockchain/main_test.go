package blockchain

import (
	"os"
	"testing"
)

// TestMain configures the package's tests to be cheap on disk.
//
// Tests open one database per case -- E7 alone opens more than twenty -- and
// BadgerDB preallocates a value log for each. At the production default that is
// gigabytes of preallocation for a few kilobytes of blocks, which fails outright
// on a nearly-full volume. Two megabytes is ample for test-sized chains.
func TestMain(m *testing.M) {
	if os.Getenv(VLOG_MB_ENV) == "" {
		os.Setenv(VLOG_MB_ENV, "2")
	}
	if os.Getenv(MEMTABLE_MB_ENV) == "" {
		os.Setenv(MEMTABLE_MB_ENV, "4")
	}
	os.Setenv("DAANVEER_QUIET_DB", "1")
	os.Exit(m.Run())
}
