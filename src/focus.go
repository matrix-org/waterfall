package main

import (
	"sync"
)

type confs struct {
	confsMu sync.RWMutex
	confs   map[string]*conf
}

type focus struct {
	name  string
	confs confs
}

func (f *focus) Init(name string) {
	f.name = name
	f.confs.confs = make(map[string]*conf)
}
