"""在指定测试目录生成不含真实用户数据的浏览器验收附件。"""
import argparse
import base64
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('directory')
args = parser.parse_args()
root = Path(args.directory)
root.mkdir(parents=True, exist_ok=True)
(root / '普通 中文.txt').write_text('附件预览验收\n<script>window.havenAttachmentExecuted=true</script>\n你好，Hermes Haven。', encoding='utf-8')
(root / '任意.bin').write_bytes(bytes(range(256)))
(root / '图片.png').write_bytes(base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aJ1sAAAAASUVORK5CYII='))
(root / '页面.html').write_text('<script>window.havenAttachmentExecuted=true</script><h1>仅作文本预览</h1>', encoding='utf-8')
with (root / '超过上限.bin').open('wb') as output:
    output.seek(50 * 1024 * 1024)
    output.write(b'x')

# 最小有效 PDF 包含交叉引用表，供浏览器原生阅读器验收。
objects = [b'<< /Type /Catalog /Pages 2 0 R >>', b'<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
           b'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 400 400] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>',
           b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>']
stream = b'BT /F1 18 Tf 35 340 Td (Hermes Haven PDF preview) Tj ET'
objects.append(b'<< /Length ' + str(len(stream)).encode() + b' >>\nstream\n' + stream + b'\nendstream')
pdf, offsets = bytearray(b'%PDF-1.4\n'), [0]
for number, content in enumerate(objects, 1):
    offsets.append(len(pdf))
    pdf.extend(str(number).encode() + b' 0 obj\n' + content + b'\nendobj\n')
xref = len(pdf)
pdf.extend(b'xref\n0 6\n0000000000 65535 f \n')
for offset in offsets[1:]:
    pdf.extend(f'{offset:010d} 00000 n \n'.encode())
pdf.extend(b'trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n' + str(xref).encode() + b'\n%%EOF\n')
(root / '报告 中文.pdf').write_bytes(pdf)
print(str(root))
