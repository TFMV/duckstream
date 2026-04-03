package metrics

import (
	"sync/atomic"
)

type Metrics struct {
	queriesRegistered   atomic.Int64
	queriesUnregistered atomic.Int64
	rowsSent            atomic.Int64
	errors              atomic.Int64
	ingestedRows        atomic.Int64
}

var global Metrics

func IncQueriesRegistered() {
	global.queriesRegistered.Add(1)
}

func IncQueriesUnregistered() {
	global.queriesUnregistered.Add(1)
}

func IncRowsSent(n int64) {
	global.rowsSent.Add(n)
}

func IncErrors() {
	global.errors.Add(1)
}

func IncIngestedRows(n int64) {
	global.ingestedRows.Add(n)
}

func Get() map[string]interface{} {
	return map[string]interface{}{
		"queries_registered":   global.queriesRegistered.Load(),
		"queries_unregistered": global.queriesUnregistered.Load(),
		"rows_sent":            global.rowsSent.Load(),
		"errors":               global.errors.Load(),
		"ingested_rows":        global.ingestedRows.Load(),
	}
}

func Reset() {
	global.queriesRegistered.Store(0)
	global.queriesUnregistered.Store(0)
	global.rowsSent.Store(0)
	global.errors.Store(0)
	global.ingestedRows.Store(0)
}
