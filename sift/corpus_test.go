package sift

// The documents the tests run against.
//
// One article that has to survive, and the shapes a web page takes when it is
// not an article. Written out rather than generated, because a filter tuned
// against a document made by repeating a word is tuned against nothing.

import "strings"

// article is ordinary Vietnamese prose of the length this stage is built to
// keep. If anything here rejects it, the threshold that did is wrong.
const article = `Bộ Giáo dục và Đào tạo vừa công bố phương án thi tốt nghiệp trung học phổ thông cho năm học tới.
Theo phương án này, thí sinh sẽ làm bốn bài thi, trong đó có hai bài bắt buộc là toán và ngữ văn.
Hai bài còn lại do thí sinh tự chọn trong số các môn đã học ở lớp mười hai, và điểm của chúng được dùng để xét tuyển đại học.
Đại diện của bộ cho biết phương án được xây dựng sau khi lấy ý kiến của các sở giáo dục và của giáo viên ở nhiều tỉnh thành.
Nhiều giáo viên nói rằng việc giảm số môn thi sẽ giúp học sinh có thời gian học sâu hơn những môn mình chọn.
Cũng có ý kiến lo ngại rằng cách xét tuyển mới sẽ khiến các trường đại học phải thay đổi cách tuyển sinh của mình trong một thời gian ngắn.
Bộ cho biết sẽ công bố đề thi tham khảo vào cuối năm nay để các trường và học sinh chuẩn bị.`

// unmarked is Vietnamese typed without tone marks, which is most of what
// people type on a phone. It is a different register and it is not a defect,
// so it has to come through this stage labeled rather than rejected.
const unmarked = `Bo Giao duc va Dao tao vua cong bo phuong an thi tot nghiep trung hoc pho thong cho nam hoc toi.
Theo phuong an nay, thi sinh se lam bon bai thi, trong do co hai bai bat buoc la toan va ngu van.
Hai bai con lai do thi sinh tu chon trong so cac mon da hoc o lop muoi hai, va diem cua chung duoc dung de xet tuyen dai hoc.
Dai dien cua bo cho biet phuong an duoc xay dung sau khi lay y kien cua cac so giao duc va cua giao vien o nhieu tinh thanh.
Nhieu giao vien noi rang viec giam so mon thi se giup hoc sinh co thoi gian hoc sau hon nhung mon minh chon.
Cung co y kien lo ngai rang cach xet tuyen moi se khien cac truong dai hoc phai thay doi cach tuyen sinh cua minh trong mot thoi gian ngan.
Bo cho biet se cong bo de thi tham khao vao cuoi nam nay de cac truong va hoc sinh chuan bi.`

// caption is a photograph caption that an extractor returned on its own.
const caption = `Người dân đi lại trên phố Hàng Bài chiều 12 tháng 8. Ảnh: Ngọc Thành.`

// menu is what a navigation bar looks like after extraction: every line a
// bullet, no sentence anywhere in it.
const menu = `- Trang chủ
- Thời sự
- Góc nhìn
- Thế giới
- Kinh doanh
- Bất động sản
- Khoa học
- Giải trí
- Thể thao
- Pháp luật
- Giáo dục
- Sức khỏe
- Đời sống
- Du lịch
- Số hóa
- Xe
- Ý kiến
- Tâm sự
- Hài
- Video
- Ảnh
- Infographics
- Podcast
- Tất cả chuyên mục
- Đăng nhập
- Đăng ký
- Liên hệ tòa soạn
- Quảng cáo
- Điều khoản sử dụng
- Chính sách bảo mật`

// listing is an index page: every entry a headline and the first half of a
// sentence, cut off where the teaser ended.
const listing = `Hà Nội sắp có thêm hai tuyến buýt nhanh, dự kiến chạy từ quý sau và ...
Giá vàng trong nước sáng nay tăng thêm ba trăm nghìn đồng mỗi lượng, trong khi ...
Cảnh sát giao thông sẽ xử lý nghiêm các trường hợp đi vào làn khẩn cấp trên ...
Trường đại học đầu tiên công bố điểm chuẩn, nhiều ngành lấy trên hai mươi lăm ...
Người dân ở nhiều tỉnh miền Trung đang chuẩn bị ứng phó với cơn bão dự báo sẽ ...
Doanh nghiệp xuất khẩu gạo nói đơn hàng cuối năm tăng mạnh so với cùng kỳ và ...
Bộ Y tế khuyến cáo người dân tiêm nhắc lại khi mùa dịch đang đến gần, nhất là ...
Đội tuyển quốc gia sẽ có trận giao hữu vào cuối tháng này tại sân vận động ...`

// looped is a page whose extractor took the same paragraph out of every block
// of a template, which is a duplicate inside one document rather than between
// two of them.
var looped = strings.Repeat(`Công ty chúng tôi chuyên cung cấp dịch vụ vận chuyển hàng hóa đi các tỉnh thành trên toàn quốc với giá cả hợp lý.
Quý khách có nhu cầu xin vui lòng liên hệ với chúng tôi để được tư vấn và báo giá chi tiết nhất.
`, 8)

// chanted is a page of one sentence repeated with a word changed each time,
// which the duplicate line measures miss and the gram measures do not.
var chanted = func() string {
	var b strings.Builder
	for _, tinh := range []string{"Hà Nội", "Hải Phòng", "Đà Nẵng", "Cần Thơ", "Huế", "Vinh", "Nam Định", "Thái Bình", "Quảng Ninh", "Bắc Ninh", "Hà Nam", "Ninh Bình"} {
		b.WriteString("Dịch vụ chuyển nhà trọn gói giá rẻ uy tín chuyên nghiệp tại " + tinh + " của công ty chúng tôi.\n")
	}
	return b.String()
}()

// english is a document in another language, long enough that nothing but the
// language checks would notice it.
const english = `The committee met on Thursday to review the draft regulation and to hear from the two working groups that had been asked to report.
Members agreed that the timetable was tight and that the second reading would have to wait until the following session.
A revised text will be circulated to the delegations before the end of the month, and comments are due two weeks after that.
The chair noted that three of the outstanding questions had been settled in correspondence since the last meeting.
No decision was taken on the funding formula, which remains with the budget committee for the time being.
The next meeting is scheduled for the first week of November at the usual venue.`

// prices is a table an extractor flattened: mostly numbers, almost no letters.
const prices = `SJC 79.200.000 79.400.000
DOJI 79.150.000 79.350.000
PNJ 79.100.000 79.300.000
Bảo Tín 79.180.000 79.380.000
SJC 79.200.000 79.450.000
DOJI 79.160.000 79.360.000
PNJ 79.120.000 79.320.000
Bảo Tín 79.190.000 79.390.000
SJC 79.210.000 79.460.000
DOJI 79.170.000 79.370.000
PNJ 79.130.000 79.330.000
Bảo Tín 79.200.000 79.400.000
SJC 79.220.000 79.470.000
DOJI 79.180.000 79.380.000
PNJ 79.140.000 79.340.000
Bảo Tín 79.210.000 79.410.000`
