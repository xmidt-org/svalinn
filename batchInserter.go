package main

import (
	"sync"
	"time"

	"github.com/Comcast/codex/db"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/semaphore"
	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
)

type batchInserter struct {
	insertQueue      chan db.Record
	inserter         db.RetryInsertService
	maxInsertWorkers int
	insertWorkers    semaphore.Interface
	maxBatchSize     int
	maxBatchWaitTime time.Duration
	wg               sync.WaitGroup
	measures         *Measures
	logger           log.Logger
}

func (b *batchInserter) batchRecords() {
	var (
		records      []db.Record
		timeToSubmit time.Time
	)
	defer b.wg.Done()
	for record := range b.insertQueue {
		// if we don't have any records, then this is our first and started
		// the timer until submitting
		if len(records) == 0 {
			timeToSubmit = time.Now().Add(b.maxBatchWaitTime)
		}

		if b.measures != nil {
			b.measures.InsertingQueue.Add(-1.0)
		}
		records = append(records, record)

		// if we have filled up the batch or if we are out of time, we insert
		// what we have
		if len(records) >= b.maxBatchSize || time.Now().After(timeToSubmit) {
			b.insertWorkers.Acquire()
			go b.insertRecords(records)
			// don't need to remake an array each time, just remove the values
			records = records[:0]
		}

	}

	// Grab all the workers to make sure they are done.
	for i := 0; i < b.maxInsertWorkers; i++ {
		b.insertWorkers.Acquire()
	}
}

func (b *batchInserter) insertRecords(records []db.Record) {
	defer b.insertWorkers.Release()
	err := b.inserter.InsertRecords(records...)
	if err != nil {
		if b.measures != nil {
			b.measures.DroppedEventsCount.With(reasonLabel, dbFailReason).Add(float64(len(records)))
		}
		logging.Error(b.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to add records to the database", logging.ErrorKey(), err.Error())
		return
	}
	logging.Debug(b.logger).Log(logging.MessageKey(), "Successfully upserted device information", "records", records)
	logging.Info(b.logger).Log(logging.MessageKey(), "Successfully upserted device information", "number of records", len(records))
}
