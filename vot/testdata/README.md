# Five real training runs

Every log here came out of a model that was actually trained. Nothing in this
directory was drawn with a formula, and the reason that matters is that a loss
curve is easy to fake badly: the noise is not symmetric, it shrinks as the model
fits, the decay is not linear, and a real spike does not go up by a constant
factor and come back down the way it went up. A detector tuned against a drawn
curve is a detector tuned against whoever drew it.

The model is a character language model over the real Vietnamese that ships in
this repository, which is the eight documents in `sang/testdata/langid/vietnamese`
and the six normalized documents in `phoi/testdata`, about seven and a half
kilobytes in total. Eight characters of context, sixteen dimensions of embedding,
one hidden layer of sixty four, a batch of sixty four, Adam with betas of 0.9 and
0.95, the gradient clipped at a norm of one, and a schedule of two hundred steps
of warmup then cosine decay from 3e-3 to 3e-4. The code is in `train_test.go` and
`go test ./vot -update` runs it again.

Each row is what a trainer logs: the step, the loss on that minibatch, the
learning rate, and the gradient norm before clipping.

| log | steps | logged | what was done to it |
| --- | --- | --- | --- |
| `on-dinh` | 4,000 | every 10 | nothing |
| `vot-len` | 4,000 | every 10 | the rate set 25 times too high for 30 steps at step 2,500 |
| `phan-ky` | 4,000 | every 10 | the rate set 400 times too high for 60 steps at step 2,500 |
| `vot-nhieu` | 40,000 | every 10 | the rate set 25 times too high for 30 steps, five times, from step 8,000 |
| `ghi-thua` | 40,000 | every 100 | the rate set 25 times too high for 300 steps at step 25,000 |

The pathology is always the same one, because it is the one that actually happens:
a resume comes back without its scheduler state and runs hot until somebody
notices.

## What these logs changed

The detector was written before they existed, and three of its numbers were wrong
in ways no amount of reading the code would have shown.

The scatter multiplier started at six. Against `vot-len` that read a real blowup,
one anybody would see by eye in the curve, as an ordinary step. Swept against all
three of the four thousand step runs, anything under three starts reporting the
clean run's own noise and anything over four and a half stops reporting the
recoverable blowup, so the constant is three and a half and the sweep is in the
package documentation rather than in somebody's memory.

`vot-nhieu` and `ghi-thua` are long enough that the model memorizes seven
kilobytes of text and the loss collapses to around a twentieth of a nat. No
pretraining run reaches that regime, but these do, and in it a band that is a
fraction of the median is a fraction of nearly nothing: `vot-nhieu` reports a
hundred and two spikes. That is the right answer rather than a broken one, and it
is why the spike count is a fault. Past a few, the curve is the finding and the
table under it is not a work list.

The best argument in the whole package came out of that run and was not planned.
Three of those hundred and two excursions are the blowups the rate caused. Sorted
by loss they do not come out on top. Sorted by gradient norm they are the top
three, above every other spike in the run, with clear air under them. That is the
whole case for keeping the gradient norm on the report and for treating a log
that does not carry one as a log that cannot answer the next question.

## What they do not show

Two of the five blowups in `vot-nhieu`, at steps 24,000 and 32,000, left almost
nothing in the loss. By then the cosine schedule has decayed far enough that
twenty five times the rate is still a small rate. The same mistake is a different
size of problem depending on when it is made, which is worth knowing before
somebody argues that a run was fine because the curve looks fine.
