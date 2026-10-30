package core

import (
	"time"

	"github.com/kotuke/lumilo/internal/engine"
)

const (
	historyMax  = 20 // Speed.History length
	averageOver = 10 // Speed.Average covers this many last entries
	// liveOver is how many probe intervals Speed.Current spans: with a
	// probe a second, about the last three seconds. A draft model emits
	// tokens in bursts, so one interval alone jumps.
	liveOver = 3
	// liveMin is the least decode time Speed.Current is measured over: a
	// shorter one gives wild values.
	liveMin = 500 * time.Millisecond
)

// speedMeter turns cumulative probe counters into Speed.
type speedMeter struct {
	base *engine.Snapshot // the last counted probe; nil = take the next as baseline
	// start is the probe a request's counters run from: the last one with
	// nothing in progress, or the one that saw the last request finish.
	start *engine.Snapshot
	// live are the tokens and decode time of the last intervals of the
	// requests in progress (liveOver), for Speed.Current.
	live  []interval
	speed Speed
}

// interval is what one probe interval added.
type interval struct {
	tokens int64
	decode time.Duration
}

// reset forgets everything. fromZero makes the next probe count against
// zero counters (a server we just started); otherwise the next probe only
// sets the baseline (a server found already running).
func (m *speedMeter) reset(fromZero bool) {
	m.speed = Speed{}
	m.base, m.start, m.live = nil, nil, nil
	if fromZero {
		m.base = &engine.Snapshot{}
		m.start = m.base
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
		// First probe or counters reset: baseline only.
		m.speed.Current, m.speed.Tokens, m.start, m.live = 0, 0, &snap, nil
		return false
	}
	dTok := s.OutputTokens - b.OutputTokens
	dDec := s.DecodeTime - b.DecodeTime
	if k := s.Completed - b.Completed; k > 0 {
		completed = true
		m.speed.LastRequest = now
		// The finished requests' speed is over all they did since start,
		// not over the last interval only: that is how fast the request
		// was. Requests finishing together share it.
		start := m.start
		if start == nil {
			start = b
		}
		tok, dec := s.OutputTokens-start.OutputTokens, s.DecodeTime-start.DecodeTime
		if tok > k && dec > 0 { // a 1-token request is the Restore ping
			rate := float64(tok) / dec.Seconds()
			for range min(k, historyMax) {
				m.speed.History = append(m.speed.History, rate)
			}
			if n := len(m.speed.History); n > historyMax {
				m.speed.History = append([]float64(nil), m.speed.History[n-historyMax:]...)
			}
			m.speed.Average = mean(m.speed.History, averageOver)
		}
		m.start = &snap
	}
	if s.Active == 0 {
		m.speed.Current, m.speed.Tokens, m.live = 0, 0, nil
		m.start = &snap
		return completed
	}
	if m.start != nil {
		m.speed.Tokens = s.OutputTokens - m.start.OutputTokens
	}
	// Only intervals with decode time count: tokens an engine shows before
	// it times them (TensorFold's first tokens in flight, which its clock
	// starts at) would read thousands of tokens a second.
	if dDec > 0 {
		m.live = append(m.live, interval{dTok, dDec})
		if len(m.live) > liveOver {
			m.live = m.live[len(m.live)-liveOver:]
		}
	}
	var tok int64
	var dec time.Duration
	for _, iv := range m.live {
		tok, dec = tok+iv.tokens, dec+iv.decode
	}
	if tok > 0 && dec >= liveMin {
		m.speed.Current = float64(tok) / dec.Seconds()
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
