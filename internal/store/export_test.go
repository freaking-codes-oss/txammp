package store

// openAt opens a store rooted at an explicit path (test helper).
func openAt(root string) (*Store, error) {
	if err := mkdirAll(root); err != nil {
		return nil, err
	}
	s := &Store{Root: root, locks: map[string]*lockEntry{}}
	return s, nil
}

func mkdirAll(dir string) error { return osMkdirAll(dir) }
