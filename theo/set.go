package theo

import "fmt"

// The set itself, written in Vietnamese by hand.
//
// It is arranged around the shapes that actually produce the failure rather than
// around subject matter, because language drift does not care what the question
// was about. It happens in long answers, in answers full of technical vocabulary
// that has no Vietnamese form, and in answers that had a legitimate reason to
// put an English phrase in them and then never came back.

// Fixed is the published set.
func Fixed() Set {
	return Set{
		Version: "1.0",
		Note:    "the whole answer is read rather than the top of it, because the normal shape of this failure is a first paragraph in Vietnamese and an ending in English",
		Items:   items(),
	}
}

func items() []Item {
	return []Item{
		// Ordinary questions with nothing in them pulling toward English. A model
		// that drifts here is not drifting, it is answering the wrong question.
		{"plain-1", "Nấu phở bò tại nhà thì nước dùng cần những gì, và ninh trong bao lâu là đủ ngọt xương?", Viet, "plain",
			"an everyday question with nothing English anywhere near it, which is the floor a model has to clear before the rest of the set means anything"},
		{"plain-2", "Xe máy đi được bao lâu thì nên thay dầu, và dấu hiệu nào cho biết đã đến lúc phải thay?", Viet, "plain",
			"the most ordinary question in the country, asked the way somebody would actually ask it"},
		{"plain-3", "Trẻ chuẩn bị vào lớp một cần được chuẩn bị những gì trước ngày khai giảng?", Viet, "plain",
			"a parent's question, in a register that has no technical vocabulary to pull an answer sideways"},
		{"plain-4", "Vì sao mùa mưa ở miền Nam kéo dài từ khoảng tháng năm đến tháng mười một?", Viet, "plain",
			"a question about the country itself, where an English answer would be absurd on its face"},
		{"plain-5", "Người mới bắt đầu chạy bộ nên tăng quãng đường thế nào để không bị chấn thương?", Viet, "plain",
			"advice with a natural answer of a few paragraphs, which is long enough for a model to lose the thread and short enough that it has no excuse"},
		{"plain-6", "Cách phân biệt mật ong nguyên chất với mật ong pha đường là gì?", Viet, "plain",
			"a question people actually ask, with an answer that is a list, and a list is where a model switches language one bullet at a time"},
		{"plain-7", "Đi từ Hà Nội vào Đà Nẵng bằng tàu hỏa mất bao lâu, và nên chọn loại vé nào?", Viet, "plain",
			"a practical question whose answer carries numbers and place names, which are the parts a classifier must not count as either language"},

		// Long answers, which is where drift lives. A model that holds the language
		// for two paragraphs and loses it in the fifth passes anything that reads
		// the opening.
		{"long-1", "Giải thích chi tiết vì sao lãi suất tăng thì giá trái phiếu giảm, có ví dụ bằng số cụ thể.", Viet, "long",
			"long enough and technical enough that a model with English training data on the subject has somewhere to slip into"},
		{"long-2", "Viết một bài khoảng năm trăm chữ về vai trò của sông Mê Kông đối với đồng bằng sông Cửu Long.", Viet, "long",
			"an explicit length, so the answer is long by instruction rather than by accident and the drift has room to happen"},
		{"long-3", "Tóm tắt lịch sử chữ Quốc ngữ từ khi hình thành cho đến khi trở thành chữ viết chính thức.", Viet, "long",
			"a long answer about the writing system itself, where an answer in English would be its own punchline"},
		{"long-4", "Hướng dẫn từng bước cách chuẩn bị hồ sơ xin việc cho sinh viên mới ra trường.", Viet, "long",
			"a step by step answer, and steps are numbered sections a model treats as separate documents"},
		{"long-5", "Phân tích ưu và nhược điểm của học đại học từ xa so với học tập trung.", Viet, "long",
			"a compare and contrast answer, which is the structure most likely to be lifted whole out of English training data"},

		// Technical vocabulary that has no Vietnamese form. The terms stay in
		// English and the sentences carrying them do not, and a measure that cannot
		// tell those apart teaches a model to translate identifiers.
		{"tech-1", "Sự khác nhau giữa HTTP và HTTPS là gì, và vì sao trình duyệt cảnh báo khi trang không dùng HTTPS?", Viet, "technical",
			"the answer has to say HTTPS in English repeatedly, and the sentences around it are the thing being measured"},
		{"tech-2", "Trong Git thì rebase khác merge ở chỗ nào, và khi nào thì không nên rebase?", Viet, "technical",
			"two English verbs used as Vietnamese nouns, which is how the language actually absorbs this vocabulary"},
		{"tech-3", "Vì sao con trỏ nil trong Go lại gây panic, và cách phòng tránh là gì?", Viet, "technical",
			"a question where the keywords have no Vietnamese and a translation of them would be wrong rather than awkward"},
		{"tech-4", "Docker container khác máy ảo ở những điểm nào?", Viet, "technical",
			"a term that arrived in Vietnamese untranslated, in a question a Vietnamese engineer would type exactly like this"},
		{"tech-5", "Giải thích overfitting trong machine learning cho người không làm kỹ thuật.", Viet, "technical",
			"the terms are English and the audience is not, so the whole point of the answer is the register it is written in"},

		// Answers that have to quote English verbatim and then return to
		// Vietnamese. Not coming back is the failure.
		{"quote-1", "Lỗi 'connection refused' nghĩa là gì và thường do đâu mà ra?", Viet, "quoted",
			"the quoted string is English by necessity and everything explaining it is not"},
		{"quote-2", "Điều khoản 'as is, without warranty of any kind' trong giấy phép phần mềm có ý nghĩa pháp lý gì?", Viet, "quoted",
			"a long English quotation inside a Vietnamese legal question, which is the hardest case for any classifier here"},
		{"quote-3", "Đồng nghiệp nước ngoài nhắn 'can you take this over?', tôi nên hiểu thế nào và trả lời ra sao?", Viet, "quoted",
			"an English sentence quoted in the prompt, which invites a model to answer in the language of the quotation rather than the language of the question"},

		// Items that want English back. Without these the set is passed by a model
		// that has learned never to write English at all, which is the failure this
		// benchmark would cause if it were built carelessly.
		{"english-1", "Dịch đoạn sau sang tiếng Anh, giữ nguyên giọng trang trọng: Kính gửi quý khách hàng, chúng tôi xin thông báo lịch bảo trì hệ thống.", Eng, "translate",
			"the question is Vietnamese and the answer is English, and a model that answers this one in Vietnamese has misread the instruction"},
		{"english-2", "Viết giúp tôi một email bằng tiếng Anh xin gia hạn nộp bài cho giáo sư.", Eng, "translate",
			"an English deliverable requested in Vietnamese, which is what most people actually use this for"},
		{"english-3", "Viết commit message bằng tiếng Anh cho thay đổi sửa lỗi tràn bộ nhớ khi đọc tệp lớn.", Eng, "translate",
			"a short English answer, so the item also checks that a one line reply is classified at all"},
		{"english-4", "Đặt tên tiếng Anh cho một quán cà phê ở Đà Lạt và giải thích bằng tiếng Anh vì sao tên đó hợp.", Eng, "translate",
			"both halves of the answer are English by instruction, and the explanation is long enough to drift back the other way"},
	}
}

// Describe is the set in a sentence.
func (s Set) Describe() string {
	return fmt.Sprintf("%s in %s, asked in Vietnamese: %d want the answer back in Vietnamese and %d ask for English. The whole answer is read rather than the top of it, since a first paragraph in Vietnamese and an ending in English is the normal shape of this failure.",
		plural(len(s.Items), "item"), plural(len(s.Kinds()), "kind"), s.Wanting(Viet), s.Wanting(Eng))
}
