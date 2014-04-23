package push

type Notification struct {
	Data map[string]string
}

func (self *Notification) IsEmpty() bool {
	if self == nil {
		return true
	}
	return len(self.Data) == 0
}
