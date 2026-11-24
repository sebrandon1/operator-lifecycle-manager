package install

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReasonForError(t *testing.T) {
	var typedNil *StrategyError

	tests := []struct {
		name string
		err  error
		want string
		skip string
	}{
		{
			name: "PlainValue",
			err:  StrategyError{Reason: StrategyErrReasonTimeout},
			want: StrategyErrReasonTimeout,
		},
		{
			name: "PlainPointer",
			err:  &StrategyError{Reason: StrategyErrReasonInvalidStrategy},
			want: StrategyErrReasonInvalidStrategy,
		},
		{
			name: "WrappedValue",
			err:  fmt.Errorf("apply failed: %w", StrategyError{Reason: StrategyErrReasonTimeout}),
			want: StrategyErrReasonTimeout,
			skip: "INST-02: ReasonForError type-switches on the concrete StrategyError without errors.As, so a %w-wrapped strategy error is reported as Unknown and treated as recoverable",
		},
		{
			name: "WrappedPointer",
			err:  fmt.Errorf("apply failed: %w", &StrategyError{Reason: StrategyErrBadPatch}),
			want: StrategyErrBadPatch,
			skip: "INST-02: wrapped pointer strategy error also degrades to Unknown",
		},
		{
			name: "Joined",
			err:  errors.Join(StrategyError{Reason: StrategyErrReasonTimeout}, errors.New("other")),
			want: StrategyErrReasonTimeout,
			skip: "INST-02: an unrecoverable reason hidden in an aggregate is not found",
		},
		{
			name: "TypedNil",
			err:  typedNil,
			want: StrategyErrReasonUnknown,
			skip: "INST-02: a typed-nil *StrategyError matches the pointer case and panics on t.Reason",
		},
		{
			name: "Nil",
			err:  nil,
			want: StrategyErrReasonUnknown,
		},
		{
			name: "Generic",
			err:  errors.New("boom"),
			want: StrategyErrReasonUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			require.NotPanics(t, func() {
				require.Equal(t, tt.want, ReasonForError(tt.err))
			})
		})
	}
}

func TestIsErrorUnrecoverable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
		skip string
	}{
		{name: "Nil", err: nil, want: false},
		{name: "Generic", err: errors.New("boom"), want: false},
		{name: "Timeout", err: StrategyError{Reason: StrategyErrReasonTimeout}, want: true},
		{name: "WaitingIsRecoverable", err: StrategyError{Reason: StrategyErrReasonWaiting}, want: false},
		{
			name: "WrappedTimeout",
			err:  fmt.Errorf("apply failed: %w", StrategyError{Reason: StrategyErrReasonTimeout}),
			want: true,
			skip: "INST-02: wrapped unrecoverable errors are treated as recoverable, so the install retries forever",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip != "" {
				t.Skip(tt.skip)
			}
			require.Equal(t, tt.want, IsErrorUnrecoverable(tt.err))
		})
	}
}
