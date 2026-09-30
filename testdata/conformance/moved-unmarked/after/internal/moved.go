package store

func (s *Store) Save() error {
	return s.legacy.Save()
}
