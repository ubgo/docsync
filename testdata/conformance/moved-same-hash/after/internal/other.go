package store

func Unrelated() {}

// ds:def id=sess-save-k7m2p4xq owner=@auth
func (s *Store) Save() error {
	return s.legacy.Save()
}
