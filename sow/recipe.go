package sow

import (
	"fmt"

	"github.com/tamnd/gao/doc"
)

// settings hashes a filter's configuration, spelled out rather than referenced,
// so that the hash in the recipe is derivable from the recipe.
func settings(s string) doc.Hash { return doc.SumString("sow filter " + s) }

// Fixed is the recipe for gao-synth, closed before a token of it exists.
//
// It is in the source rather than in a file somebody edits because that is the
// only version of pre-commitment that means anything: changing it is a diff on a
// pull request, with a reviewer, rather than a file edited the afternoon the
// numbers came out.
//
// The generator is a model with no gao in its training data, and the source is
// the top of the quality distribution rather than the whole corpus, because
// rephrasing text that was already poor produces poor text in a new voice. The
// four styles are four registers rather than four temperatures: a register moves
// the syntax and the vocabulary, and a temperature only moves the tail.
func Fixed() Recipe {
	return Recipe{
		Version:   "1.0",
		Generator: "qwen3-235b-a22b-instruct",
		Revision:  "2026-04-11",
		ReadGao:   false,

		Source:       "gao-edu",
		SourceDigest: doc.SumString("gao-edu slice, gao-v1.0"),
		Target:       150_000_000_000,

		Styles: []Style{
			{
				Name:   "bao-chi",
				Prompt: "Viết lại đoạn văn sau theo văn phong báo chí tiếng Việt: câu ngắn, thông tin đặt ở đầu, không bình luận. Giữ nguyên mọi số liệu, tên riêng và ngày tháng. Không thêm thông tin không có trong bản gốc.\n\n{{item}}",
				Note:   "the register most Vietnamese prose on the web is already written in, kept so the rephrase does not drift away from what the corpus looks like",
			},
			{
				Name:   "giang-giai",
				Prompt: "Viết lại đoạn văn sau như một bài giảng cho người mới bắt đầu: giải thích từng khái niệm khi nó xuất hiện lần đầu, dùng ví dụ cụ thể. Giữ nguyên mọi số liệu, tên riêng và ngày tháng. Không thêm thông tin không có trong bản gốc.\n\n{{item}}",
				Note:   "the explanatory register, which is where the long dependencies are and which the crawled web has the least of",
			},
			{
				Name:   "hoi-dap",
				Prompt: "Chuyển đoạn văn sau thành một cuộc hỏi đáp tiếng Việt tự nhiên giữa hai người: mỗi câu hỏi bám vào một ý trong bản gốc, mỗi câu trả lời chỉ dùng thông tin có trong bản gốc. Giữ nguyên mọi số liệu, tên riêng và ngày tháng.\n\n{{item}}",
				Note:   "dialog, which is the shape of most of what anybody will actually ask the model, and which almost nothing in the natural corpus is written as",
			},
			{
				Name:   "tom-luoc",
				Prompt: "Tóm tắt đoạn văn sau bằng tiếng Việt trong khoảng một phần ba độ dài, giữ lại các ý chính theo đúng thứ tự xuất hiện. Giữ nguyên mọi số liệu, tên riêng và ngày tháng. Không thêm nhận định của người viết.\n\n{{item}}",
				Note:   "compression, which teaches the model what in a document is load bearing, and the only style here that produces fewer tokens than it reads",
			},
		},

		Decoding: Decoding{
			Temperature: 0.8,
			TopP:        0.95,
			MaxTokens:   4096,
			Seed:        20260401,
		},

		Filters: []Filter{
			{
				Name:       "vi-only",
				ConfigHash: settings("fasttext lid, vi >= 0.90, per document"),
				Why:        "a generator asked for Vietnamese answers in English more often than anybody expects, particularly at the end of a long document",
			},
			{
				Name:       "faithful",
				ConfigHash: settings("numbers, dates, and named entities in the output must appear in the source"),
				Why:        "a rephrase that invents a number is not a rephrase, and it is the failure that survives every other gate here because the text reads perfectly",
			},
			{
				Name:       "not-a-copy",
				ConfigHash: settings("minhash jaccard vs source, reject >= 0.90"),
				Why:        "output that is the source back again spends GPU hours to add a duplicate, which the dedup pass would then remove anyway",
			},
			{
				Name:       "degenerate",
				ConfigHash: settings("repeated 5-gram fraction >= 0.20, or output shorter than 40 syllables"),
				Why:        "the loop a sampled generator falls into, which is fluent for a paragraph and then is not text at all",
			},
			{
				Name:       "refusal",
				ConfigHash: settings("assistant preamble and refusal patterns, vi and en"),
				Why:        "the model talking about the task instead of doing it, which is training data for a habit nobody wants",
			},
			{
				Name:       "contamination",
				ConfigHash: settings("13-gram overlap against the benchmark roster"),
				Why:        "a generated document that reproduces a benchmark item puts the answer in the training set, and the evaluation afterward is scoring memorization",
			},
		},

		Roster: "pick-2026.08",
		Note:   "the source is the educational slice rather than the corpus, because a rephrase of poor text is poor text in a new voice",
	}
}

// Describe is the paragraph that goes at the top of the dataset card, written
// out here so the card and the README cannot drift apart.
func (r Recipe) Describe() string {
	return fmt.Sprintf(
		"%s is model-generated Vietnamese: %s rephrasing %s in %s, at temperature %v with seed %d, filtered by %s. It is not natural text and it is never counted as any.",
		doc.SourceSynth, r.Generator, r.Source, plural(len(r.Styles), "register"),
		r.Decoding.Temperature, r.Decoding.Seed, plural(len(r.Filters), "gate"))
}
