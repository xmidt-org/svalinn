package main

import (
	"time"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/goph/emperror"
)

func (r *RequestHandler) handleRecords() {
	var (
		records      []db.Record
		timeToSubmit time.Time
	)
	defer r.wg.Done()
	for record := range r.insertQueue {
		// if we don't have any records, then this is our first and started
		// the timer until submitting
		if len(records) == 0 {
			timeToSubmit = time.Now().Add(r.maxBatchWaitTime)
		}

		if r.measures != nil {
			r.measures.InsertingQeue.Add(-1.0)
		}
		records = append(records, record)

		// if we have filled up the batch or if we are out of time, we insert
		// what we have
		if len(records) >= r.maxBatchSize || time.Now().After(timeToSubmit) {
			r.insertWorkers.Acquire()
			go r.insertRecords(records)
			// don't need to remake an array each time, just remove the values
			records = records[:0]
		}

	}

	// Grab all the workers to make sure they are done.
	for i := 0; i < r.maxInsertWorkers; i++ {
		r.insertWorkers.Acquire()
	}
}

func (r *RequestHandler) insertRecords(records []db.Record) {
	defer r.insertWorkers.Release()
	err := r.inserter.InsertRecords(records...)
	if err != nil {
		r.measures.DroppedEventsCount.With(reasonLabel, dbFailReason).Add(float64(len(records)))
		logging.Error(r.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to add records to the database", logging.ErrorKey(), err.Error())
		return
	}
	logging.Debug(r.logger).Log(logging.MessageKey(), "Successfully upserted device information", "records", records)
	logging.Info(r.logger).Log(logging.MessageKey(), "Successfully upserted device information", "number of records", len(records))
}
