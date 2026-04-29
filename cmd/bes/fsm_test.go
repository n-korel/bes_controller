package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
)

func TestFSM_ClientResetCancelsCallSetup(t *testing.T) {
	resetCh := make(chan struct{}, 10)
	pressCh := make(chan struct{}, 1)
	answers := make(chan answerEvent, 10)
	conversations := make(chan string, 10)

	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      200 * time.Millisecond,
		ClientQueryRetryInterval: 10 * time.Millisecond,
		ClientQueryMaxRetries:    1,
	}}

	var st atomic.Uint32

	var enteredOnce sync.Once
	enteredCallSetupCh := make(chan struct{})
	var canceledOnce sync.Once
	sipCanceledCh := make(chan struct{})

	allowDialCh := make(chan struct{})
	finishCh := make(chan struct{})

	fsm := besFSM{
		cfg:        cfg,
		logger:    nopLogger{},
		state:     &st,
		resetCh:   resetCh,
		pressCh:   pressCh,
		answers:   answers,
		autoPressAfterFirstReset: true,
		sendQueryOnce: func() error {
			return nil
		},
		runSIP: func(
			ctx context.Context,
			logger besLogger,
			cfg config.Bes,
			domain string,
			sipID string,
			conversations <-chan string,
			dialEstablished chan<- struct{},
		) error {
			enteredOnce.Do(func() { close(enteredCallSetupCh) })

			select {
			case <-ctx.Done():
				canceledOnce.Do(func() { close(sipCanceledCh) })
				return ctx.Err()
			case <-allowDialCh:
				if dialEstablished != nil {
					dialEstablished <- struct{}{}
				}
			}

			select {
			case <-ctx.Done():
				canceledOnce.Do(func() { close(sipCanceledCh) })
				return ctx.Err()
			case <-finishCh:
				return nil
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fsm.Run(ctx, conversations) }()

	// Trigger UNREGISTERED -> REGISTRATION_IDLE and auto-press.
	resetCh <- struct{}{}
	go func() {
		time.Sleep(20 * time.Millisecond)
		answers <- answerEvent{
			answer: &protocol.ClientAnswer{NewIP: "1.2.3.4", SipID: "42"},
			from:   nil,
		}
	}()

	select {
	case <-enteredCallSetupCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting CALL_SETUP entry")
	}

	// ClientReset during CALL_SETUP must cancel SIP setup.
	resetCh <- struct{}{}

	deadline := time.After(500 * time.Millisecond)
	for st.Load() != stateRegistrationIdle {
		select {
		case <-deadline:
			t.Fatalf("state=%d want REGISTRATION_IDLE (%d)", st.Load(), stateRegistrationIdle)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	select {
	case <-sipCanceledCh:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected SIP setup context cancellation on ClientReset")
	}

	cancel()
	<-done
}

func TestFSM_ClientResetIgnoredInCall(t *testing.T) {
	resetCh := make(chan struct{}, 10)
	pressCh := make(chan struct{}, 1)
	answers := make(chan answerEvent, 10)
	conversations := make(chan string, 10)

	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      200 * time.Millisecond,
		ClientQueryRetryInterval: 10 * time.Millisecond,
		ClientQueryMaxRetries:    1,
	}}

	var st atomic.Uint32

	var enteredOnce sync.Once
	enteredCallSetupCh := make(chan struct{})

	var canceledOnce sync.Once
	sipCanceledCh := make(chan struct{})

	allowDialCh := make(chan struct{})
	finishCh := make(chan struct{})

	fsm := besFSM{
		cfg:              cfg,
		logger:          nopLogger{},
		state:           &st,
		resetCh:         resetCh,
		pressCh:         pressCh,
		answers:         answers,
		autoPressAfterFirstReset: true,
		sendQueryOnce: func() error {
			return nil
		},
		runSIP: func(
			ctx context.Context,
			logger besLogger,
			cfg config.Bes,
			domain string,
			sipID string,
			conversations <-chan string,
			dialEstablished chan<- struct{},
		) error {
			enteredOnce.Do(func() { close(enteredCallSetupCh) })

			select {
			case <-ctx.Done():
				canceledOnce.Do(func() { close(sipCanceledCh) })
				return ctx.Err()
			case <-allowDialCh:
				if dialEstablished != nil {
					dialEstablished <- struct{}{}
				}
			}

			select {
			case <-ctx.Done():
				canceledOnce.Do(func() { close(sipCanceledCh) })
				return ctx.Err()
			case <-finishCh:
				return nil
			}
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fsm.Run(ctx, conversations) }()

	resetCh <- struct{}{}
	go func() {
		time.Sleep(20 * time.Millisecond)
		answers <- answerEvent{
			answer: &protocol.ClientAnswer{NewIP: "1.2.3.4", SipID: "42"},
			from:   nil,
		}
	}()

	select {
	case <-enteredCallSetupCh:
	case <-ctx.Done():
		t.Fatal("timeout waiting CALL_SETUP entry")
	}

	// Allow dialEstablished -> FSM should transition to IN_CALL.
	close(allowDialCh)

	deadline := time.After(500 * time.Millisecond)
	for st.Load() != stateInCall {
		select {
		case <-deadline:
			t.Fatalf("state=%d want IN_CALL (%d)", st.Load(), stateInCall)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// ClientReset in IN_CALL must be ignored (no SIP cancellation).
	resetCh <- struct{}{}

	select {
	case <-sipCanceledCh:
		t.Fatalf("SIP setup context was cancelled on ClientReset in IN_CALL")
	case <-time.After(150 * time.Millisecond):
		// ok: not cancelled yet
	}

	close(finishCh)

	deadline2 := time.After(500 * time.Millisecond)
	for st.Load() != stateRegistrationIdle {
		select {
		case <-deadline2:
			t.Fatalf("state=%d want REGISTRATION_IDLE (%d)", st.Load(), stateRegistrationIdle)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	cancel()
	<-done
}

