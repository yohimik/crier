package publish

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

// retryNotReady runs attempt, asking again while notReady recognises the
// failure and the deadline allows.
//
// It exists for one narrow class of refusal: a platform has said the media is
// processed, the final post-creation call disagrees, and the refusal itself
// guarantees nothing was created. That guarantee is the whole contract — a
// matcher must only ever recognise errors the platform documents as creating
// nothing, because everything else keeps the never-repeat rule that guards
// against double posts.
//
// It happened on a real release: Instagram polled a story container FINISHED
// and refused the publish seconds later with "not ready, wait a moment".
func retryNotReady(ctx context.Context, log zerolog.Logger, interval, budget time.Duration,
	what string, attempt func() error, notReady func(error) bool) error {
	deadline := time.Now().Add(budget)
	for {
		err := attempt()
		if err == nil {
			return nil
		}
		if !notReady(err) || time.Now().After(deadline) {
			return err
		}
		log.Debug().Str("call", what).Msg("the media is not ready yet; asking again")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
