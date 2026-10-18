//go:build !linux && !darwin

package term

// State is a saved terminal mode; unused on this platform.
type State struct{}

func IsTerminal(uintptr) bool            { return false }
func MakeRaw(uintptr) (*State, error)    { return nil, ErrUnsupported }
func Restore(uintptr, *State) error      { return nil }
func Size(uintptr) (w, h int, err error) { return 0, 0, ErrUnsupported }
