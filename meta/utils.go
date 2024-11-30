package meta

func UpdateListener(l *Listener) {
	ListenersRW.Lock()
	if ListenersMap[l.Streamid] == nil {
		ListenersMap[l.Streamid] = make(map[int]*Listener)
	}
	ListenersMap[l.Streamid][l.Index] = l
	ListenersRW.Unlock()
}

func GetListener(streamid string, index int) *Listener {
	ListenersRW.Lock()
	defer ListenersRW.Unlock()
	stream := ListenersMap[streamid]
	if stream != nil {
		return stream[index]
	}
	return nil
}

func RemoveI(s []any, i int) []any {
	s[i] = s[len(s)-1]
	return s[:len(s)-1]
}
