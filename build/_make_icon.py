# -*- coding: utf-8 -*-
"""生成「框不住的老郭」应用图标：
深色圆角底 + 白色开口画框（右上缺口）+ 一道冲出框的红笔触。
输出 1024 appicon.png 和多尺寸 icon.ico
"""
from PIL import Image, ImageDraw
import os

S = 1024
BG = (26, 26, 31, 255)        # 深墨底
FG = (245, 245, 240, 255)     # 米白框
ACCENT = (230, 57, 70, 255)   # 硬核红

img = Image.new("RGBA", (S, S), (0, 0, 0, 0))
d = ImageDraw.Draw(img)

# 深色圆角底
d.rounded_rectangle([0, 0, S - 1, S - 1], radius=200, fill=BG)

# 画框参数：左上左三边完整，右上角开口
x0, y0, x1, y1 = 236, 268, 812, 788
w = 62  # 笔画宽

# 上边（右端留缺口，不到 x1）
d.rounded_rectangle([x0, y0, x0 + 400, y0 + w], radius=w // 2, fill=FG)
# 下边（完整）
d.rounded_rectangle([x0, y1 - w, x1, y1], radius=w // 2, fill=FG)
# 左边（完整）
d.rounded_rectangle([x0, y0, x0 + w, y1], radius=w // 2, fill=FG)
# 右边（下段，上端留缺口）
d.rounded_rectangle([x1 - w, y1 - 260, x1, y1], radius=w // 2, fill=FG)

# 冲出框的红笔触：45° 斜线，从框内穿右上缺口飞出
p_in = (600, 610)
p_out = (880, 300)
d.line([p_in, p_out], fill=ACCENT, width=w)
r = w // 2
d.ellipse([p_in[0] - r, p_in[1] - r, p_in[0] + r, p_in[1] + r], fill=ACCENT)
d.ellipse([p_out[0] - r, p_out[1] - r, p_out[0] + r, p_out[1] + r], fill=ACCENT)

out_dir = os.path.dirname(os.path.abspath(__file__))
png_path = os.path.join(out_dir, "appicon.png")
img.save(png_path)

# 多尺寸 ico
ico_sizes = [(256, 256), (128, 128), (64, 64), (48, 48), (32, 32), (16, 16)]
ico_path = os.path.join(out_dir, "icon.ico")
img.save(ico_path, format="ICO", sizes=ico_sizes)

print("saved:", png_path)
print("saved:", ico_path)
