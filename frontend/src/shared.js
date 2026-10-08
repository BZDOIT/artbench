// Wails 绑定调用助手：所有页面共用
export function api(method, ...args) {
  if (!window.go || !window.go.main || !window.go.main.App) {
    return Promise.reject(new Error('后端未就绪（请在 Wails 窗口里运行）'))
  }
  return window.go.main.App[method](...args)
}

export const THEMES = [
  { k: 'dark', n: '黑' },
  { k: 'light', n: '白' },
  { k: 'sepia', n: '护眼' },
  { k: 'auto', n: '跟随' },
]

// 图片尺寸（官方）：档位 + 8 种比例
export const IMAGE_SIZES = ['1K', '2K', '3K', '4K']
export const IMAGE_RATIOS = ['1:1', '3:4', '4:3', '16:9', '9:16', '2:3', '3:2', '21:9']

export const ASPECTS = ['16:9', '9:16', '4:3', '3:4', '1:1', '21:9']
export const ASPECT_HINT = {
  '16:9': '横屏 · 视频号 / B站',
  '9:16': '竖屏 · 抖音 / 快手',
  '4:3': '复古方横屏',
  '3:4': '竖版方屏 · 小红书',
  '1:1': '正方形 · 朋友圈',
  '21:9': '超宽 · 电影感',
}

export function fmtSize(bytes) {
  if (bytes > 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB'
  if (bytes > 1024) return (bytes / 1024).toFixed(0) + ' KB'
  return bytes + ' B'
}

export function fmtTime(ts) {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const p = n => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
