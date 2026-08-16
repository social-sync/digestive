package export

import "time"

// rowTickInterval is how often (in rows) exportTable reports progress to the
// Reporter. Row counts can reach the millions, so we throttle rather than
// emit an event per row.
const rowTickInterval = 2048

// Reporter receives progress events over the course of an export run. Every
// method is called from the goroutine that runs the export, in order; a
// Reporter that drives a concurrent UI should forward events to it rather than
// do slow work inline. A nil Reporter (the default) discards everything.
type Reporter interface {
	// Start fires once, after the plan is built and the run directory exists.
	Start(runName, dir string, tables []string)
	// TableStart fires when a table begins exporting.
	TableStart(table string)
	// TableRows reports the cumulative row count for the current table. It is
	// throttled (see rowTickInterval), not called once per row.
	TableRows(table string, rows int64)
	// TableDone fires when a table has been fully written.
	TableDone(table string, rows int64, elapsed time.Duration)
	// Warn surfaces a non-fatal issue (e.g. json_anonymise fallbacks).
	Warn(msg string)
	// Failed fires when a table errors; the run stops after this.
	Failed(table string, err error)
	// Done fires once when every table has succeeded.
	Done(runName, dir string, elapsed time.Duration)
}

// nopReporter is the default Reporter: it discards every event.
type nopReporter struct{}

func (nopReporter) Start(string, string, []string)         {}
func (nopReporter) TableStart(string)                      {}
func (nopReporter) TableRows(string, int64)                {}
func (nopReporter) TableDone(string, int64, time.Duration) {}
func (nopReporter) Warn(string)                            {}
func (nopReporter) Failed(string, error)                   {}
func (nopReporter) Done(string, string, time.Duration)     {}
