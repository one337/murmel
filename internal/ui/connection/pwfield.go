package connection

// passwordField tracks whether the connect form's password field holds a
// value the keyring may be updated from. While a saved connection's password
// is still being looked up, the empty field means "not loaded yet", not
// "no password", so connecting must leave the keyring alone.
type passwordField struct {
	gen     uint64
	pending bool
}

// startLookup marks the field as waiting for a keyring lookup and returns
// the token to hand to lookupDone.
func (f *passwordField) startLookup() uint64 {
	f.gen++
	f.pending = true
	return f.gen
}

// lookupDone reports whether the looked-up password should be put into the
// field. A failed lookup leaves the field pending; a lookup overtaken by a
// newer one or by the user's typing is dropped.
func (f *passwordField) lookupDone(gen uint64, ok bool) bool {
	if gen != f.gen || !f.pending || !ok {
		return false
	}
	f.pending = false
	return true
}

// edited records that the user changed the field; their input counts and
// a lookup still on its way no longer replaces it.
func (f *passwordField) edited() {
	f.gen++
	f.pending = false
}

// known reports whether the field's content may be saved to (or, when
// empty, removed from) the keyring.
func (f *passwordField) known() bool { return !f.pending }
