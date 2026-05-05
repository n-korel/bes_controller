package bes

import (
	"context"
)

type sipCallOrchestrator struct {
	f *besFSM
}

func (o sipCallOrchestrator) run(ctx context.Context, sipID, newIP string, conversations <-chan string) uint32 {
	f := o.f

	sipCtx, cancelSIP := context.WithCancel(ctx)
	defer cancelSIP()
	defer f.state.Store(stateRegistrationIdle)

	dialEstablishedCh := make(chan struct{}, 1)
	sipDoneCh := make(chan error, 1)

	f.state.Store(stateCallSetup)
	f.drainConversations(conversations)

	go func() {
		sipDoneCh <- f.runSIP(sipCtx, f.logger, f.cfg, newIP, sipID, conversations, dialEstablishedCh)
	}()

	f.drainPress()

	logSIPDone := func(err error) {
		if err == nil {
			return
		}
		if sipCtx.Err() != nil || ctx.Err() != nil {
			return
		}
		f.logger.Warn("sip call ended with error", "err", err)
	}

	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			cancelSIP()
			err := <-sipDoneCh
			logSIPDone(err)
			return stateRegistrationIdle
		case <-f.resetCh:
			// ClientReset can happen multiple times while the call is being set up (e.g. user presses again).
			// It must not cancel SIP; we just drain it to avoid reprocessing stale events.
			f.drainResetBurst()
		case <-dialEstablishedCh:
			f.state.Store(stateInCall)
			// ClientReset in IN_CALL must be ignored. Drain any buffered resets
			// at the moment we enter the call so they can't be re-processed after BYE.
			f.drainResetBurst()

			for ctx.Err() == nil {
				select {
				case <-ctx.Done():
					cancelSIP()
					err := <-sipDoneCh
					logSIPDone(err)
					return stateRegistrationIdle
				case <-f.resetCh:
					f.drainResetBurst()
				case err := <-sipDoneCh:
					logSIPDone(err)
					f.drainPress()
					return stateRegistrationIdle
				}
			}
			return stateRegistrationIdle
		case err := <-sipDoneCh:
			logSIPDone(err)
			f.drainPress()
			return stateRegistrationIdle
		}
	}

	return stateRegistrationIdle
}
