package main

import (
	"context"
	"sync/atomic"

	"bucis-bes_simulator/internal/infra/config"
)

type besLogger interface {
	Info(string, ...any)
	Warn(string, ...any)
}

type runSIPFunc func(
	ctx context.Context,
	logger besLogger,
	cfg config.Bes,
	domain string,
	sipID string,
	conversations <-chan string,
	dialEstablished chan<- struct{},
) error

type besFSM struct {
	cfg config.Bes
	// logger is passed to both doPress() and runSIP().
	logger besLogger

	state *atomic.Uint32

	sendQueryOnce func() error
	runSIP        runSIPFunc

	resetCh       <-chan struct{}
	pressCh       <-chan struct{}
	answers       <-chan answerEvent
	autoPressAfterFirstReset bool
}

func (f *besFSM) Run(ctx context.Context, conversations <-chan string) error {
	st := stateUnregistered
	f.state.Store(st)

	autoPressDone := !f.autoPressAfterFirstReset
	for ctx.Err() == nil {
		switch st {
		case stateUnregistered:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-f.resetCh:
				st = stateRegistrationIdle
				f.state.Store(st)
				if !autoPressDone {
					autoPressDone = true
					// UNREGISTERED -> REGISTRATION_IDLE -> (press) REGISTRATION_QUERYING.
					st = stateRegistrationQuery
					f.state.Store(st)
					f.drainAnswers()
					f.drainPress()
					sipID, newIP, err := doPress(ctx, f.logger, f.cfg, f.sendQueryOnce, f.answers)
					if err != nil {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						f.logger.Warn("press failed", "err", err)
						st = stateRegistrationIdle
						f.state.Store(st)
						continue
					}

					// Enter SIP setup.
					f.drainConversations(conversations)
					st = stateCallSetup
					f.state.Store(st)
					st = f.runSIPAndTrack(ctx, sipID, newIP, conversations)
				}
			}

		case stateRegistrationIdle:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-f.resetCh:
				// Already registered: no state change.
			case <-f.pressCh:
				st = stateRegistrationQuery
				f.state.Store(st)

				// Ignore any queued events while we are waiting for ec_client_answer.
				f.drainPress()
				f.drainReset()
				f.drainAnswers()

				sipID, newIP, err := doPress(ctx, f.logger, f.cfg, f.sendQueryOnce, f.answers)
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					f.logger.Warn("press failed", "err", err)
					st = stateRegistrationIdle
					f.state.Store(st)
					continue
				}

				// Enter SIP setup.
				f.drainConversations(conversations)
				st = stateCallSetup
				f.state.Store(st)
				st = f.runSIPAndTrack(ctx, sipID, newIP, conversations)
			}

		default:
			// These states are handled inside runSIPAndTrack(), QUERYING is synchronous.
			st = stateRegistrationIdle
			f.state.Store(st)
		}
	}

	return ctx.Err()
}

func (f *besFSM) drainAnswers() {
	for {
		select {
		case <-f.answers:
		default:
			return
		}
	}
}

func (f *besFSM) drainConversations(conversations <-chan string) {
	for {
		select {
		case <-conversations:
		default:
			return
		}
	}
}

func (f *besFSM) drainPress() {
	for {
		select {
		case <-f.pressCh:
		default:
			return
		}
	}
}

func (f *besFSM) drainReset() {
	for {
		select {
		case <-f.resetCh:
		default:
			return
		}
	}
}

func (f *besFSM) runSIPAndTrack(ctx context.Context, sipID, newIP string, conversations <-chan string) uint32 {
	// During SIP setup we must react to ClientReset (CALL_SETUP) and ignore it (IN_CALL).
	sipCtx, cancelSIP := context.WithCancel(ctx)
	defer cancelSIP()
	defer f.state.Store(stateRegistrationIdle)

	dialEstablishedCh := make(chan struct{}, 1)
	sipDoneCh := make(chan error, 1)

	// Mark SIP setup as active before starting SIP goroutine.
	// This prevents state races if dialEstablished triggers immediately.
	f.state.Store(stateCallSetup)

	go func() {
		sipDoneCh <- f.runSIP(sipCtx, f.logger, f.cfg, newIP, sipID, conversations, dialEstablishedCh)
	}()

	// Drop any press that could have been queued just before SIP started.
	f.drainPress()

	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			cancelSIP()
			<-sipDoneCh
			return stateRegistrationIdle
		case <-f.resetCh:
			// Abort SIP attempt if reset arrived during SIP setup (CALL_SETUP).
			cancelSIP()
			err := <-sipDoneCh
			_ = err
			// Ignore any buffered press for the next registration cycle.
			f.drainPress()
			return stateRegistrationIdle
		case <-dialEstablishedCh:
			// We reached IN_CALL: now ClientReset must be ignored.
			f.state.Store(stateInCall)

			for ctx.Err() == nil {
				select {
				case <-ctx.Done():
					cancelSIP()
					<-sipDoneCh
					return stateRegistrationIdle
				case <-f.resetCh:
					// Ignored in IN_CALL.
				case <-sipDoneCh:
					// Conversation ended / SIP hung up.
					f.drainPress()
					return stateRegistrationIdle
				}
			}
			return stateRegistrationIdle
		case <-sipDoneCh:
			// SIP finished early (e.g., setup failed). Treat as done and go back to registration.
			f.drainPress()
			return stateRegistrationIdle
		}
	}

	return stateRegistrationIdle
}

