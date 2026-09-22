package db

import (
	"errors"
	"time"

	"github.com/psyb0t/ctxerrors"
	"gorm.io/gorm"
)

const callbackPrefix = "vibecheck:metrics:"

// gormRegister is the shape of a gorm callback processor's Register method.
// Naming it lets the six operations be a table instead of six near-identical
// closures.
type gormRegister func(name string, fn func(*gorm.DB)) error

// registerObserver wires timing callbacks around every gorm operation.
//
// A nil observer is the documented off switch, so a deployment without
// metrics does not pay for the callbacks at all.
func registerObserver(database *gorm.DB, observer Observer) error {
	if observer == nil {
		return nil
	}

	callbacks := database.Callback()

	pairs := []struct {
		operation string
		before    gormRegister
		after     gormRegister
	}{
		{
			operation: "create",
			before:    callbacks.Create().Before("gorm:create").Register,
			after:     callbacks.Create().After("gorm:create").Register,
		},
		{
			operation: "query",
			before:    callbacks.Query().Before("gorm:query").Register,
			after:     callbacks.Query().After("gorm:query").Register,
		},
		{
			operation: "update",
			before:    callbacks.Update().Before("gorm:update").Register,
			after:     callbacks.Update().After("gorm:update").Register,
		},
		{
			operation: "delete",
			before:    callbacks.Delete().Before("gorm:delete").Register,
			after:     callbacks.Delete().After("gorm:delete").Register,
		},
		{
			operation: "row",
			before:    callbacks.Row().Before("gorm:row").Register,
			after:     callbacks.Row().After("gorm:row").Register,
		},
		{
			operation: "raw",
			before:    callbacks.Raw().Before("gorm:raw").Register,
			after:     callbacks.Raw().After("gorm:raw").Register,
		},
	}

	for _, pair := range pairs {
		err := registerPair(observer, pair.operation, pair.before, pair.after)
		if err != nil {
			return err
		}
	}

	return nil
}

// registerPair installs the timing callbacks for one operation.
func registerPair(
	observer Observer,
	operation string,
	before, after gormRegister,
) error {
	beforeFn, afterFn := observerCallbacks(observer, operation)

	if err := before(callbackPrefix+operation+"_before", beforeFn); err != nil {
		return ctxerrors.Wrapf(
			err, "register the %s before callback", operation,
		)
	}

	if err := after(callbackPrefix+operation+"_after", afterFn); err != nil {
		return ctxerrors.Wrapf(
			err, "register the %s after callback", operation,
		)
	}

	return nil
}

// observerCallbacks builds the start and finish hooks for one operation.
//
// The start time rides on the statement through InstanceSet, so concurrent
// statements never read each other's clock.
func observerCallbacks(
	observer Observer,
	operation string,
) (func(*gorm.DB), func(*gorm.DB)) {
	startKey := callbackPrefix + operation + ":started_at"

	before := func(tx *gorm.DB) {
		tx.InstanceSet(startKey, time.Now())
	}

	after := func(tx *gorm.DB) {
		value, ok := tx.InstanceGet(startKey)
		if !ok {
			return
		}

		startedAt, ok := value.(time.Time)
		if !ok {
			return
		}

		observer.ObserveDB(
			operation,
			isOperationFailure(tx.Error),
			time.Since(startedAt).Seconds(),
		)
	}

	return before, after
}

// isOperationFailure reports whether a statement error is a real fault.
//
// A lookup that found nothing is an ordinary answer, not a database failure.
// Counting it as one makes the error rate track how often callers ask for
// rows that do not exist, which is exactly the signal an operator would
// misread during a normal day.
func isOperationFailure(err error) bool {
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}

	return true
}
