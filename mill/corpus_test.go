package mill

// The documents the tests in this package run against.
//
// They are one news article and the shapes it takes on the way around the web,
// which is the case deduplication exists for, plus two documents that have
// nothing to do with it. Written out rather than generated, because a near
// duplicate produced by deleting every seventh character is not a near duplicate
// anybody publishes and a threshold tuned against one would be tuned against
// nothing.

// article is the piece as its first publisher ran it.
const article = `Hà Nội bước vào đợt nắng nóng đầu tiên của mùa hè. Nhiệt độ ngoài trời có lúc lên tới 39 độ C, và ngành điện cho biết lượng tiêu thụ trong ngày đã vượt mức của cùng kỳ năm ngoái. Người dân được khuyến cáo hạn chế ra đường trong khoảng từ 11 giờ trưa đến 15 giờ chiều. Bản tin do đài truyền hình phát lúc 19 giờ nói đợt nóng này còn kéo dài đến cuối tuần.`

// syndicated is the same piece on a second site: its own headline on the front,
// a credit line at the end, and the body untouched.
const syndicated = `Nắng nóng gay gắt ở thủ đô. Hà Nội bước vào đợt nắng nóng đầu tiên của mùa hè. Nhiệt độ ngoài trời có lúc lên tới 39 độ C, và ngành điện cho biết lượng tiêu thụ trong ngày đã vượt mức của cùng kỳ năm ngoái. Người dân được khuyến cáo hạn chế ra đường trong khoảng từ 11 giờ trưa đến 15 giờ chiều. Bản tin do đài truyền hình phát lúc 19 giờ nói đợt nóng này còn kéo dài đến cuối tuần. Theo báo điện tử, đăng lại có dẫn nguồn.`

// corrected is the piece after somebody fixed a number in it, which is the
// closest a pair of documents gets without being the same document.
const corrected = `Hà Nội bước vào đợt nắng nóng đầu tiên của mùa hè. Nhiệt độ ngoài trời có lúc lên tới 40 độ C, và ngành điện cho biết lượng tiêu thụ trong ngày đã vượt mức của cùng kỳ năm ngoái. Người dân được khuyến cáo hạn chế ra đường trong khoảng từ 11 giờ trưa đến 15 giờ chiều. Bản tin do đài truyền hình phát lúc 19 giờ nói đợt nóng này còn kéo dài đến cuối tuần.`

// retyped is the same document with the punctuation and the capitals a second
// content management system gave it, and one syllable spelled the other way.
const retyped = `HÀ NỘI bước vào đợt nắng nóng đầu tiên của mùa hè! Nhiệt độ ngoài trời có lúc lên tới 39 độ C, và ngành điện cho biết lượng tiêu thụ trong ngày đã vượt mức của cùng kỳ năm ngoái... Người dân được khuyến cáo hạn chế ra đường trong khoảng từ 11 giờ trưa đến 15 giờ chiều. Bản tin do đài truyền hình phát lúc 19 giờ nói đợt nóng này còn kéo dài đến cuối tuần.`

// quoted is a forum post that quotes two sentences of the article and answers
// them. It shares real text with the article and is not a copy of it, which is
// the pair a threshold set too low destroys.
const quoted = `Bạn nào ở nhà thì đọc cái này: nhiệt độ ngoài trời có lúc lên tới 39 độ C, và ngành điện cho biết lượng tiêu thụ trong ngày đã vượt mức của cùng kỳ năm ngoái. Mình thấy trưa nay ra đường đúng là không chịu nổi thật, quạt với điều hòa chạy cả ngày mà vẫn nóng. Mọi người ở khu vực khác thì sao, có ai đi làm giờ đó không.`

// river and reform have nothing to do with the article or with each other.
const river = `Sông Hồng bắt nguồn từ Vân Nam và chảy qua các tỉnh phía bắc trước khi đổ ra vịnh Bắc Bộ. Phù sa của con sông này là lý do đồng bằng ở hạ lưu trở thành vùng trồng lúa lớn thứ hai cả nước. Người Việt đã đắp đê dọc hai bờ từ rất sớm, và hệ thống đê ấy vẫn đang được giữ đến ngày nay.`

const reform = `Năm 1986, đại hội đảng quyết định chuyển sang nền kinh tế nhiều thành phần. Kể từ đó, kim ngạch xuất khẩu tăng đều qua từng năm, và hàng dệt may trở thành nhóm hàng đứng đầu. Số liệu được thống kê từ năm 1990 đến nay cho thấy xu hướng ấy chưa đảo chiều.`

// jaccard is the similarity the signature is estimating, computed the slow and
// exact way. The tests use it to check the estimate rather than to replace it.
func jaccard(a, b string) float64 {
	sa, sb := shingleSet(a), shingleSet(b)
	shared := 0
	for h := range sa {
		if sb[h] {
			shared++
		}
	}
	union := len(sa) + len(sb) - shared
	if union == 0 {
		return 1
	}
	return float64(shared) / float64(union)
}

func shingleSet(text string) map[uint64]bool {
	out := map[uint64]bool{}
	for h := range Shingles(Key(text)) {
		out[h] = true
	}
	return out
}
