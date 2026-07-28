package dashboard

// Ring is a fixed-capacity FIFO retaining the newest values.
type Ring[T any] struct {
	values []T
	next   int
	full   bool
}

func NewRing[T any](capacity int) *Ring[T] {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring[T]{values: make([]T, capacity)}
}

func (r *Ring[T]) Push(value T) {
	r.values[r.next] = value
	r.next = (r.next + 1) % len(r.values)
	if r.next == 0 {
		r.full = true
	}
}

func (r *Ring[T]) Values() []T {
	if !r.full {
		return append([]T(nil), r.values[:r.next]...)
	}
	out := make([]T, 0, len(r.values))
	out = append(out, r.values[r.next:]...)
	out = append(out, r.values[:r.next]...)
	return out
}
