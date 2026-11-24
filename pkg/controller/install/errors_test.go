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
		},
		{
			name: "WrappedPointer",
			err:  fmt.Errorf("apply failed: %w", &StrategyError{Reason: StrategyErrBadPatch}),
			want: StrategyErrBadPatch,
		},
		{
			name: "Joined",
			err:  errors.Join(StrategyError{Reason: StrategyErrReasonTimeout}, errors.New("other")),
			want: StrategyErrReasonTimeout,
		},
		{
			name: "TypedNil",
			err:  typedNil,
			want: StrategyErrReasonUnknown,
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
