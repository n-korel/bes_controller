package bes

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bucis-bes_simulator/internal/control/protocol"
	"bucis-bes_simulator/internal/infra/config"
)

func TestFSM_PressDuringFailedQueryRetries(t *testing.T) {
	resetCh := make(chan struct{}, 10)
	pressCh := make(chan struct{}, 1)
	answers := make(chan answerEvent, 10)
	conversations := make(chan string, 10)

	cfg := config.Bes{EC: config.EC{
		ClientAnswerTimeout:      60 * time.Millisecond,
		ClientQueryRetryInterval: 10 * time.Millisecond,
		ClientQueryMaxRetries:    1,
	}}

	var st atomic.Uint32

	var sendCalls atomic.Int32
	enteredQuery := make(chan struct{})
	var enteredOnce sync.Once

	fsm := besFSM{
		cfg:    cfg,
		logger: nopLogger{},
		state:  &st,

		resetCh:                  resetCh,
		pressCh:                  pressCh,
		answers:                  answers,
		autoPressAfterFirstReset: false,
		sendQueryOnce: func() error {
			sendCalls.Add(1)
			enteredOnce.Do(func() { close(enteredQuery) })
			return nil
		},
		runSIP: func(
			ctx context.Context,
			logger Logger,
			cfg config.Bes,
			domain string,
			sipID string,
			conversations <-chan string,
			dialEstablished chan<- struct{},
		) error {
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fsm.Run(ctx, conversations) }()

	// UNREGISTERED -> REGISTRATION_IDLE.
	resetCh <- struct{}{}

	deadlineIdle := time.After(500 * time.Millisecond)
	for st.Load() != stateRegistrationIdle {
		select {
		case <-deadlineIdle:
			t.Fatalf("state=%d want REGISTRATION_IDLE (%d)", st.Load(), stateRegistrationIdle)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Start querying.
	pressCh <- struct{}{}

	select {
	case <-enteredQuery:
	case <-ctx.Done():
		t.Fatal("timeout waiting for query start")
	}

	// Second press during query retries: after failure it must start a new query (not discarded).
	select {
	case pressCh <- struct{}{}:
	default:
	}

	// doPress should fail (no answers) and FSM should return to IDLE.
	deadlineIdle2 := time.After(700 * time.Millisecond)
	for st.Load() != stateRegistrationIdle {
		select {
		case <-deadlineIdle2:
			t.Fatalf("state=%d want REGISTRATION_IDLE (%d)", st.Load(), stateRegistrationIdle)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	deadlineSecondQuery := time.After(500 * time.Millisecond)
	for sendCalls.Load() < 2 {
		select {
		case <-deadlineSecondQuery:
			t.Fatalf("sendQueryOnce calls=%d want 2 (press during failed query must retry)", sendCalls.Load())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	cancel()
	<-done
}

func TestFSM_ClientResetIgnoredDuringCallSetup(t *testing.T) {
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
		cfg:                      cfg,
		logger:                   nopLogger{},
		state:                    &st,
		resetCh:                  resetCh,
		pressCh:                  pressCh,
		answers:                  answers,
		autoPressAfterFirstReset: true,
		sendQueryOnce: func() error {
			return nil
		},
		runSIP: func(
			ctx context.Context,
			logger Logger,
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

	// ClientReset during CALL_SETUP must be ignored (must not cancel SIP).
	resetCh <- struct{}{}

	select {
	case <-sipCanceledCh:
		t.Fatalf("SIP setup context was cancelled on ClientReset during CALL_SETUP")
	case <-time.After(150 * time.Millisecond):
		// ok: not cancelled
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
		cfg:                      cfg,
		logger:                   nopLogger{},
		state:                    &st,
		resetCh:                  resetCh,
		pressCh:                  pressCh,
		answers:                  answers,
		autoPressAfterFirstReset: true,
		sendQueryOnce: func() error {
			return nil
		},
		runSIP: func(
			ctx context.Context,
			logger Logger,
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

	// Real-world case: user presses again during setup; reset arrives before dialEstablished.
	resetCh <- struct{}{}

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

func TestFSM_ClientResetDuringSetup_BeforeDialEstablished(t *testing.T) {
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
		cfg:                      cfg,
		logger:                   nopLogger{},
		state:                    &st,
		resetCh:                  resetCh,
		pressCh:                  pressCh,
		answers:                  answers,
		autoPressAfterFirstReset: true,
		sendQueryOnce: func() error {
			return nil
		},
		runSIP: func(
			ctx context.Context,
			logger Logger,
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

	// First reset is being processed while in CALL_SETUP; second one is already buffered.
	resetCh <- struct{}{}
	resetCh <- struct{}{}

	// Must not cancel SIP setup context on resets during CALL_SETUP.
	select {
	case <-sipCanceledCh:
		t.Fatalf("SIP setup context was cancelled on ClientReset during CALL_SETUP")
	case <-time.After(150 * time.Millisecond):
	}

	// Allow dialEstablished -> FSM should transition to IN_CALL (resets must not block it).
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

func TestBES_AutoPress_TriggersExactlyOnce(t *testing.T) {
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
	var sendCalls atomic.Int32

	fsm := besFSM{
		cfg:                      cfg,
		logger:                   nopLogger{},
		state:                    &st,
		resetCh:                  resetCh,
		pressCh:                  pressCh,
		answers:                  answers,
		autoPressAfterFirstReset: true,
		sendQueryOnce: func() error {
			sendCalls.Add(1)
			return nil
		},
		runSIP: func(
			ctx context.Context,
			logger Logger,
			cfg config.Bes,
			domain string,
			sipID string,
			conversations <-chan string,
			dialEstablished chan<- struct{},
		) error {
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- fsm.Run(ctx, conversations) }()

	// Отправить два ClientReset — убедиться, что ClientQuery отправлен ровно один раз.
	resetCh <- struct{}{}
	resetCh <- struct{}{}
	go func() {
		time.Sleep(10 * time.Millisecond)
		answers <- answerEvent{answer: &protocol.ClientAnswer{NewIP: "1.2.3.4", SipID: "42"}}
	}()

	deadline := time.After(500 * time.Millisecond)
	for sendCalls.Load() < 1 {
		select {
		case <-deadline:
			t.Fatalf("sendQueryOnce calls=%d want 1", sendCalls.Load())
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Дать времени "проявиться" лишнему автонажатию (если бы оно было).
	time.Sleep(50 * time.Millisecond)
	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("sendQueryOnce calls=%d want 1", got)
	}

	cancel()
	<-done
}
