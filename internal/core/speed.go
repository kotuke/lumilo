package core

import (
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

const (
	historyMax  = 20 // Speed.History length
	averageOver = 10 // Speed.Average covers this many last entries
)

// speedMeter turns cumulative probe counters into Speed.
type speedMeter struct {
	base  *engine.Snapshot // the last counted probe; nil = take the next as baseline
	speed Speed
}

// reset forgets everything. fromZero makes the next probe count against
// zero counters (a server we just started); otherwise the next probe only
// sets the baseline (a server found already running).
func (m *speedMeter) reset(fromZero bool) {
	m.speed = Speed{}
	m.base = nil
	if fromZero {
		m.base = &engine.Snapshot{}
	}
}

// add counts one probe. It reports whether a request finished since the
// last counted probe.
func (m *speedMeter) add(s engine.Snapshot, now time.Time) (completed bool) {
	if !s.Reachable || !s.Authorized {
		return false // counters are not readable; keep the baseline
	}
	m.speed.FirstToken = s.FirstToken
	if s.PrefillTime > 0 {
		m.speed.Read = float64(s.PrefillTokens) / s.PrefillTime.Seconds()
	}
	b := m.base
	snap := s
	m.base = &snap
	if b == nil || s.Completed < b.Completed || s.OutputTokens < b.OutputTokens || s.DecodeTime < b.DecodeTime {
		m.speed.Current = 0 // first probe or counters reset: baseline only
		return false
	}
	dTok := s.OutputTokens - b.OutputTokens
	dDec := s.DecodeTime - b.DecodeTime
	var rate float64
	if dTok > 0 && dDec > 0 {
		rate = float64(dTok) / dDec.Seconds()
	}
	if k := s.Completed - b.Completed; k > 0 {
		completed = true
		m.speed.LastRequest = now
		if dTok > 1 && rate > 0 { // a 1-token request is the Restore ping
			for range min(k, historyMax) {
				m.speed.History = append(m.speed.History, rate)
			}
			if n := len(m.speed.History); n > historyMax {
				m.speed.History = append([]float64(nil), m.speed.History[n-historyMax:]...)
			}
			m.speed.Average = mean(m.speed.History, averageOver)
		}
	}
	switch {
	case s.Active == 0:
		m.speed.Current = 0
	case rate > 0:
		m.speed.Current = rate
	}
	return completed
}

// mean is the mean of the last n values of xs; 0 when xs is empty.
func mean(xs []float64, n int) float64 {
	if len(xs) > n {
		xs = xs[len(xs)-n:]
	}
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func (m *speedMeter) view() Speed {
	sp := m.speed
	sp.History = append([]float64(nil), m.speed.History...)
	return sp
}
