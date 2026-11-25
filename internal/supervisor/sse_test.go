package supervisor

import "testing"

func TestSSEDropsSlowAndCaps(t *testing.T) {
	h := NewHub()
	var cancels []func()
	for i := 0; i < 16; i++ {
		_, cancel := h.Subscribe()
		cancels = append(cancels, cancel)
	}
	if h.Len() != 16 {
		t.Fatal(h.Len())
	}
	ch, cancel := h.Subscribe()
	defer cancel()
	if h.Len() != 16 {
		t.Fatal(h.Len())
	}
	slow, _ := h.Subscribe()
	for i := 0; i < 20; i++ {
		h.Publish(Event{Name: "state", Data: "x"})
	}
	got := 0
	drain := func(c <-chan Event) {
		for {
			select {
			case _, ok := <-c:
				if !ok {
					return
				}
				got++
			default:
				return
			}
		}
	}
	drain(ch)
	drain(slow)
	if got == 0 {
		t.Fatal("no delivery")
	}
	for _, c := range cancels {
		c()
	}
}
