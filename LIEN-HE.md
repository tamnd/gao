# gaobot

`gaobot` là trình thu thập dữ liệu của dự án [gao](https://github.com/tamnd/gao), một dự án mã nguồn mở xây dựng kho văn bản tiếng Việt để huấn luyện mô hình ngôn ngữ. Nếu bạn tìm thấy `gaobot` trong log máy chủ của mình, trang này giải thích nó là gì và bạn cần làm gì.

Bản tiếng Anh ở cuối trang.

## Tình trạng hiện tại

`gaobot` đang chạy. Những gì nó tải về được công bố công khai tại [open-index/vitweb](https://huggingface.co/datasets/open-index/vitweb): địa chỉ trang, tên miền, thời điểm tải, quyết định của `robots.txt` và mọi số đo dùng để đánh giá trang. Những trang bị loại nằm tại [open-index/vitweb-rejects](https://huggingface.co/datasets/open-index/vitweb-rejects) kèm công đoạn và lý do đã loại chúng.

Kho `open-index/vitweb` nay mang theo cả nội dung trang: phần bài viết dạng văn bản thuần, chính bài viết đó dạng markdown, và toàn bộ trang dạng markdown. Ban đầu chúng tôi chỉ công bố địa chỉ và số đo, nay công bố cả nội dung, theo đúng cách Common Crawl và các kho ngữ liệu web dựng từ nó vẫn làm: trang truy cập được công khai, `robots.txt` được tôn trọng, khai báo giữ quyền TDM được tôn trọng, và mỗi dòng đều mang theo địa chỉ gốc để bất kỳ ai cũng kiểm chứng được và để chính bạn chỉ đích danh trang cần gỡ. Bản quyền vẫn thuộc về người viết, chúng tôi không nhận và không chuyển giao quyền gì cả.

Nếu bạn không muốn nội dung của mình nằm trong đó, phần gỡ nội dung bên dưới là cách làm, và chúng tôi thực hiện chứ không đánh giá yêu cầu.

## Nhận diện

Mỗi lượt truy cập đều gửi đúng một chuỗi User-Agent, có dạng:

```
gaobot/0.1.0 (+https://github.com/tamnd/gao/blob/main/LIEN-HE.md)
```

Số phiên bản thay đổi theo bản dựng, phần còn lại thì không. Chúng tôi không đổi User-Agent, không giả dạng trình duyệt, và không đổi địa chỉ IP để tránh bị chặn. Nếu bạn thấy một chuỗi User-Agent nào khác tự xưng là `gaobot`, đó không phải chúng tôi.

## Chặn `gaobot`

Cách nhanh nhất là `robots.txt`. Chúng tôi đọc nó trước mỗi máy chủ và tôn trọng những gì nó nói.

Chặn hoàn toàn:

```
User-agent: gaobot
Disallow: /
```

Chặn một phần:

```
User-agent: gaobot
Disallow: /thanh-vien/
Disallow: /tim-kiem
```

Giãn nhịp truy cập, tính bằng giây giữa hai lượt:

```
User-agent: gaobot
Crawl-delay: 30
```

Một quy tắc viết cho `gaobot` sẽ thay thế hoàn toàn khối `User-agent: *`, nên nếu bạn viết riêng cho chúng tôi thì hãy viết đủ. Chúng tôi cũng đọc `X-Robots-Tag` trong HTTP header, thẻ `<meta name="robots">`, và khai báo TDM theo chuẩn [TDMRep](https://www.w3.org/community/tdmrep/) tại `/.well-known/tdmrep.json`.

Nếu bạn muốn chặn ngay lập tức mà không sửa `robots.txt`, trả về mã 403 hoặc 429 cho User-Agent này cũng được. Chúng tôi coi đó là một câu trả lời chứ không phải một lỗi tạm thời, và sẽ dừng lại.

## Gỡ nội dung đã thu thập

Nếu nội dung của bạn đã nằm trong kho và bạn muốn gỡ, hãy mở một issue tại:

**https://github.com/tamnd/gao/issues/new?labels=takedown**

Trong issue, xin cho biết:

- tên miền hoặc danh sách URL cần gỡ
- bạn là chủ sở hữu hay người đại diện, và ở tư cách nào
- có cần gỡ khỏi các bản phát hành đã công bố hay chỉ dừng thu thập về sau

Bạn không cần nêu lý do. Chúng tôi không đánh giá yêu cầu gỡ nội dung, chúng tôi thực hiện.

Thời gian phản hồi mục tiêu là 72 giờ kể từ lúc issue được mở, và thời gian thực tế của từng yêu cầu được ghi lại công khai tại [GO-BO.toml](GO-BO.toml) trong kho mã nguồn. Việc dừng thu thập có hiệu lực ngay khi tên miền được đưa vào danh sách chặn. Việc gỡ khỏi một bản phát hành đã công bố cần thêm thời gian vì phải dựng lại bản phát hành đó, và issue sẽ mở cho đến khi xong.

Nếu bạn không muốn công khai yêu cầu của mình, issue vẫn là cách duy nhất hiện tại. Đây là dự án mã nguồn mở của một người, không có hộp thư riêng để hứa trả lời, và chúng tôi thà nói thẳng điều đó còn hơn đăng một địa chỉ email không ai đọc.

## Chúng tôi thu thập gì

Văn bản tiếng Việt trên web công khai: diễn đàn, báo điện tử và kho lưu trữ tin, văn bản pháp quy, tài liệu giáo dục. Chúng tôi không đăng nhập vào đâu cả, không vượt tường phí, không lấy nội dung sau trang đăng nhập, và không thu thập dữ liệu cá nhân làm mục tiêu.

Chúng tôi không thu thập nội dung của một trang đã tự khai báo giữ quyền TDM. Khai báo đó được ghi lại theo từng trang và được tôn trọng ở chỗ ghi dữ liệu, chứ không chỉ ở chỗ tải về.

## Nhịp truy cập

Mặc định là mỗi máy chủ một kết nối tại một thời điểm, và một khoảng nghỉ giữa các lượt. Nếu `robots.txt` của bạn khai báo `Crawl-delay` dài hơn, chúng tôi dùng con số của bạn. Nếu máy chủ trả về 429 hoặc 503, chúng tôi lùi lại chứ không thử lại ngay.

Nếu `gaobot` gây tải nặng cho máy chủ của bạn, đó là lỗi của chúng tôi. Xin mở issue và ghi rõ tên miền, chúng tôi sẽ giảm nhịp hoặc dừng hẳn với tên miền đó.

---

## In English

`gaobot` is the crawler for [gao](https://github.com/tamnd/gao), an open source project building a Vietnamese text corpus for language model training.

**It is running.** What it fetches is published at [open-index/vitweb](https://huggingface.co/datasets/open-index/vitweb): the address, the host, the fetch time, the robots decision and every measurement the page was judged on. Pages that were turned away are at [open-index/vitweb-rejects](https://huggingface.co/datasets/open-index/vitweb-rejects) with the stage and the reason that turned them away.

**That repo now carries the page text too**, as plain text, as markdown, and as the whole page in markdown. We published addresses and measurements only to begin with and we publish the pages now, under the posture Common Crawl publishes under and the corpora built on it inherit: publicly reachable, `robots.txt` honored, TDM reservations honored, and the address on every row so the claim can be checked and so you can name the page you want removed. Copyright stays with whoever wrote the page. We are not claiming a grant and none was made to us. If you would rather your content was not in there, the takedown section below is how, and we act on requests rather than judge them.

**Identifying it.** Every request sends one User-Agent string, `gaobot/VERSION (+https://github.com/tamnd/gao/blob/main/LIEN-HE.md)`. The version changes between builds and nothing else changes. We do not rotate user agents, spoof browsers, or rotate IP addresses to get around a block.

**Blocking it.** `robots.txt` works and we read it before every host. `User-agent: gaobot` followed by `Disallow: /` stops us entirely. A block written for `gaobot` replaces the `User-agent: *` block rather than adding to it, so write it in full. We also read `X-Robots-Tag`, the robots meta tag, and TDMRep declarations at `/.well-known/tdmrep.json`. Returning 403 or 429 to this agent also works, and we treat it as an answer rather than as a transient error.

**Takedowns.** Open an issue at https://github.com/tamnd/gao/issues/new?labels=takedown with the domain or the URLs, whether you are the owner or acting for them, and whether you need removal from published releases or only a stop to future crawling. You do not need to give a reason. The target response time is 72 hours from the issue being opened and the real time for each request is recorded in public, in [GO-BO.toml](GO-BO.toml) in the repository. Stopping the crawl takes effect as soon as the domain is on the block list. Removal from a published release takes longer because the release has to be rebuilt, and the issue stays open until it is.

**Load.** One connection per host at a time by default, with a delay between requests, and your `Crawl-delay` wins if it is longer. A 429 or a 503 makes us back off rather than retry. If `gaobot` is loading your server, that is our fault: open an issue naming the domain and we will slow down or stop.
