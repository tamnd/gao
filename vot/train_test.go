package vot_test

// The logs in testdata are real training runs, and this is what produced them.
//
// A stability protocol tested against a curve somebody drew with a formula tests
// the formula. Loss curves have a shape that is hard to fake and easy to fake
// badly: the noise is not symmetric, it shrinks as the model fits, the decay is
// not linear, and a real spike does not go up by a constant factor and come back
// down the way it went up. So these logs come off a real model, trained with real
// gradients on the real Vietnamese that ships in this repo, and the spike in them
// comes from a real cause rather than from multiplying a number.
//
// Run go test ./vot -update to train them again, then read the diff.

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "train the logs in testdata again from the text in this repo")

// The model is small on purpose. What is being fixed here is the shape of a loss
// curve, and the shape of a loss curve is a property of the optimizer, the
// schedule and the batch, all three of which are the same at this size as at a
// size nobody can run in a test.
const (
	context = 8
	embed   = 16
	hidden  = 64
	batch   = 64

	peak  = 3e-3
	floor = 3e-4
	warm  = 200
	clip  = 1.0
)

// corpus reads every piece of real Vietnamese in the repo. It is not much text,
// so the model fits it and the curve comes down the way an overfitting run comes
// down, which is a real shape and is labeled as one in the testdata README.
func corpus(t *testing.T) []rune {
	t.Helper()

	var b strings.Builder
	for _, pat := range []string{"../sang/testdata/langid/vietnamese/*.txt", "../phoi/testdata/*.out"} {
		paths, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(text)
			b.WriteString("\n")
		}
	}
	if b.Len() < 4096 {
		t.Fatalf("the corpus behind these logs is %d bytes, which is not a training run", b.Len())
	}
	return []rune(b.String())
}

// net is a character language model: a context window of embeddings, one hidden
// layer, and a softmax over the alphabet. Everything is one flat slice so that
// the gradient norm and the optimizer are each one loop over it.
type net struct {
	v          int
	p, g, m, u []float64
	e, w1, b1  int
	w2, b2     int
}

func newNet(v int, r *rand.Rand) *net {
	n := &net{v: v}
	n.e = 0
	n.w1 = n.e + v*embed
	n.b1 = n.w1 + context*embed*hidden
	n.w2 = n.b1 + hidden
	n.b2 = n.w2 + hidden*v
	size := n.b2 + v

	n.p = make([]float64, size)
	n.g = make([]float64, size)
	n.m = make([]float64, size)
	n.u = make([]float64, size)
	for i := n.e; i < n.w1; i++ {
		n.p[i] = r.NormFloat64() * 0.1
	}
	for i := n.w1; i < n.b1; i++ {
		n.p[i] = r.NormFloat64() / math.Sqrt(context*embed)
	}
	for i := n.w2; i < n.b2; i++ {
		n.p[i] = r.NormFloat64() / math.Sqrt(hidden)
	}
	return n
}

// step runs one minibatch forward and backward and returns the mean loss over it
// and the gradient norm before clipping, which is the pair a real trainer logs
// and the pair that tells one kind of spike from another.
func (n *net) step(data []int, r *rand.Rand) (float64, float64) {
	for i := range n.g {
		n.g[i] = 0
	}

	h := make([]float64, hidden)
	dh := make([]float64, hidden)
	logit := make([]float64, n.v)
	var loss float64

	for range batch {
		at := r.IntN(len(data)-context-1) + context
		want := data[at]

		for j := range hidden {
			s := n.p[n.b1+j]
			for c := range context {
				id := data[at-context+c]
				for d := range embed {
					s += n.p[n.e+id*embed+d] * n.p[n.w1+(c*embed+d)*hidden+j]
				}
			}
			h[j] = math.Tanh(s)
		}

		top := math.Inf(-1)
		for k := range n.v {
			s := n.p[n.b2+k]
			for j := range hidden {
				s += h[j] * n.p[n.w2+j*n.v+k]
			}
			logit[k] = s
			top = math.Max(top, s)
		}
		var sum float64
		for k := range n.v {
			logit[k] = math.Exp(logit[k] - top)
			sum += logit[k]
		}
		loss -= math.Log(logit[want] / sum)

		for j := range hidden {
			dh[j] = 0
		}
		for k := range n.v {
			d := logit[k] / sum
			if k == want {
				d--
			}
			d /= batch
			n.g[n.b2+k] += d
			for j := range hidden {
				n.g[n.w2+j*n.v+k] += h[j] * d
				dh[j] += n.p[n.w2+j*n.v+k] * d
			}
		}
		for j := range hidden {
			d := dh[j] * (1 - h[j]*h[j])
			n.g[n.b1+j] += d
			for c := range context {
				id := data[at-context+c]
				for e := range embed {
					n.g[n.w1+(c*embed+e)*hidden+j] += n.p[n.e+id*embed+e] * d
					n.g[n.e+id*embed+e] += n.p[n.w1+(c*embed+e)*hidden+j] * d
				}
			}
		}
	}

	var norm float64
	for _, g := range n.g {
		norm += g * g
	}
	return loss / batch, math.Sqrt(norm)
}

// apply is Adam with the gradient clipped at a fixed norm, which is what every
// run at this project's scale does and is the reason a spike shows up in the
// loss rather than as a single step off the end of the world.
func (n *net) apply(lr, norm float64, at int) {
	scale := 1.0
	if norm > clip {
		scale = clip / norm
	}
	const b1, b2, eps = 0.9, 0.95, 1e-8
	c1 := 1 - math.Pow(b1, float64(at+1))
	c2 := 1 - math.Pow(b2, float64(at+1))
	for i, g := range n.g {
		g *= scale
		n.m[i] = b1*n.m[i] + (1-b1)*g
		n.u[i] = b2*n.u[i] + (1-b2)*g*g
		n.p[i] -= lr * (n.m[i] / c1) / (math.Sqrt(n.u[i]/c2) + eps)
	}
}

// rate is warmup and then cosine decay, which is the schedule in the plan.
func rate(at, steps int) float64 {
	if at < warm {
		return peak * float64(at+1) / warm
	}
	t := float64(at-warm) / float64(steps-warm)
	return floor + (peak-floor)*0.5*(1+math.Cos(math.Pi*t))
}

// train runs the model and returns the log. wrong is the pathology: it is
// multiplied into the learning rate at a given step, so a run where it always
// returns one is a run where nothing went wrong.
func train(t *testing.T, seed uint64, steps, logs int, wrong func(at int) float64) []string {
	t.Helper()

	text := corpus(t)
	id := map[rune]int{}
	data := make([]int, 0, len(text))
	for _, r := range text {
		if _, ok := id[r]; !ok {
			id[r] = len(id)
		}
		data = append(data, id[r])
	}

	r := rand.New(rand.NewPCG(seed, seed*7919+1))
	n := newNet(len(id), r)

	out := make([]string, 0, steps/logs)
	for at := range steps {
		lr := rate(at, steps) * wrong(at)
		loss, norm := n.step(data, r)
		n.apply(lr, norm, at)
		if at%logs == 0 {
			row, err := json.Marshal(map[string]any{
				"step":      at,
				"loss":      math.Round(loss*1e6) / 1e6,
				"lr":        math.Round(lr*1e9) / 1e9,
				"grad_norm": math.Round(norm*1e6) / 1e6,
			})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, string(row))
		}
	}
	return out
}

// hot is the pathology: a learning rate multiplied by mult for dur steps from
// step at, which is a resume that came back without its scheduler state.
func hot(mult float64, at, dur int) func(int) float64 {
	return func(now int) float64 {
		if now >= at && now < at+dur {
			return mult
		}
		return 1
	}
}

// The logs are the things that happen to a run: nothing, something that came
// back, something that did not, the same thing five times, and a run where all
// of it may have happened under logging too coarse to have held it.
func TestUpdateTheTrainingLogs(t *testing.T) {
	if !*update {
		t.Skip("run go test ./vot -update to train the logs in testdata again")
	}

	for _, c := range []struct {
		name        string
		seed        uint64
		steps, logs int
		wrong       func(int) float64
	}{
		{"on-dinh", 1, 4000, 10, func(int) float64 { return 1 }},
		// A resume that came back without its scheduler state and ran twenty
		// five times too hot for thirty steps, which is the most common way a
		// production run spikes and the only one that is anybody's fault.
		{"vot-len", 1, 4000, 10, hot(25, 2500, 30)},
		// The same mistake, larger and left running longer.
		{"phan-ky", 1, 4000, 10, hot(400, 2500, 60)},
		// The same mistake made five times, which is a run whose curve is the
		// finding rather than a run the protocol is handling.
		{"vot-nhieu", 1, 40_000, 10, func(at int) float64 {
			for _, from := range []int{8_000, 12_000, 16_000, 24_000, 32_000} {
				if at >= from && at < from+30 {
					return 25
				}
			}
			return 1
		}},
		// The same run again, ten times longer and logged a hundredth as often,
		// which is what a log looks like when somebody turned the logging down
		// to keep the dashboard readable.
		{"ghi-thua", 1, 40_000, 100, hot(25, 25_000, 300)},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rows := train(t, c.seed, c.steps, c.logs, c.wrong)
			path := filepath.Join("testdata", c.name+".jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			fmt.Printf("%s: %d rows\n", path, len(rows))
		})
	}
}
