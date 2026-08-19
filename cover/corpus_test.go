package cover

// The documents the tests run against.
//
// Two kinds, and the second kind is the one that decides whether this stage is
// usable. Pages that hold personal data are easy to write and easy to catch.
// Pages that hold numbers which are not personal data are what a corpus is
// mostly made of, and a detector that cannot tell them apart puts holes in the
// middle of ordinary Vietnamese sentences.

// advert is a classified listing, which is the densest personal data on the
// Vietnamese web: a name, a phone number, an address, and a plate, all in one
// short block written by the person they belong to.
const advert = `Bán căn hộ 2 phòng ngủ, diện tích 68m2, tại Số 25 đường Nguyễn Huệ, phường Bến Nghé, quận 1, TP Hồ Chí Minh.
Giá 5.200.000.000 đồng, có thương lượng cho khách thiện chí.

Liên hệ chính chủ anh Nguyễn Văn Minh, điện thoại 0912 345 678, hoặc email minh.nguyen@gmail.com.
Xe đưa đón xem nhà biển số 51F-123.45, đi lại thuận tiện trong nội thành.`

// contract is the shape a form takes after extraction: field labels and values,
// with the old and the new national ID formats side by side.
const contract = `Bên A: Công ty TNHH Thương mại Hoàng Long
Mã số thuế: 0311234567
Người đại diện: Trần Thị Hương
CCCD số 079187654321 cấp ngày 12/03/2021
CMND cũ: 023456789
Điện thoại liên hệ: 028 3823 4567`

// article is a news story that names people and quotes numbers, and holds
// nothing anybody's privacy depends on. Everything in it has to survive.
const article = `Chủ tịch Hồ Chí Minh đọc bản Tuyên ngôn độc lập ngày 2 tháng 9 năm 1945 tại quảng trường Ba Đình.
Đường Lê Lợi ở quận 1 được đặt tên từ năm 1955 và đã đổi tên ba lần trước đó.
Theo Tổng cục Thống kê, dân số cả nước đạt 100.300.000 người vào cuối năm 2023.
Giá vàng miếng SJC sáng nay là 79.200.000 đồng mỗi lượng, tăng ba trăm nghìn so với hôm qua.
Nhà thơ Nguyễn Du sinh năm 1765 và mất năm 1820, để lại Truyện Kiều gồm 3254 câu thơ.`

// prices is a page of numbers that is nothing but numbers, which is what a
// detector matching digit runs turns into a page of tags.
const prices = `Mã cổ phiếu VNM giá 68.500 khối lượng 1.234.500 cổ phiếu.
Doanh thu quý ba đạt 15.234.000.000 đồng, lợi nhuận sau thuế 2.100.000.000 đồng.
Tổng vốn đầu tư của dự án là 1234567890 đồng theo quyết định phê duyệt.
Số hiệu văn bản 105/2020/TT-BTC ban hành ngày 03/12/2020.
Chuyến bay VN 1546 khởi hành lúc 07 30 và hạ cánh lúc 09 45.`

// forum is a comment thread, where the personal data arrives unannounced and
// the surrounding text is ordinary conversation.
const forum = `Mình mua ở chỗ này lâu rồi, thấy cũng được, giao hàng khá nhanh.
Ai cần thì cứ gọi 0987654321 nhé, chủ shop tên Phạm Thu Hà, nói là mình giới thiệu.
Địa chỉ shop ở Số 12 phố Hàng Bông, phường Hàng Gai, quận Hoàn Kiếm, Hà Nội.`
