<template>
  <div class="approot">
    <header>
      <div class="brand">角色设定表工作台 <small>桌面版 v{{ version }}</small></div>
      <div class="status">
        <span v-if="cfg"><span :class="['dot', cfg.key_ready ? '' : 'bad']"></span>key {{ cfg.key_count }} 把</span>
        <span v-if="cfg">{{ cfg.model }}</span>
        <span v-if="cfg">风格 {{ cfg.style_count }} · 陷阱 {{ cfg.trap_count }}</span>
        <select class="thsel" v-model="theme" @change="setTheme(theme)" title="主题">
          <option v-for="t in THEMES" :key="t.k" :value="t.k">{{ t.n }}</option>
        </select>
        <button class="thbtn" @click="showGallery = true" title="上游风格编号画廊（随风格库热更新）">🖼 画廊</button>
        <button class="thbtn" @click="openSettings">⚙ 设置</button>
      </div>
    </header>

    <nav class="navtabs">
      <button v-for="p in PAGES" :key="p.k" :class="['mtab', {on: page===p.k}]" @click="page = p.k">{{ p.n }}</button>
    </nav>

    <main class="pagemain">
      <!-- 设定表 / 自由出图：两个顶级页签共用一个组件，pageMode 驱动内部表单切换 -->
      <div v-show="page==='sheet'||page==='free'">
        <StyleStrip :styles="styles" :groups="groups" :curNo="curNo" @select="selectStyle"/>
        <SheetTab ref="sheet" :cfg="cfg" :styles="styles" :colors="colors"
          :curNo="curNo" :curStyle="curStyle" :page-mode="page"
          @toast="showToast" @style-cleared="clearStyle"/>
      </div>

      <!-- 图文卡 -->
      <div v-show="page==='card'">
        <CardTab ref="card" :cfg="cfg" :styles="styles" :colors="colors" :layouts="layouts"
          @toast="showToast"/>
      </div>

      <!-- 素材库 -->
      <div v-show="page==='library'">
        <LibraryTab ref="library" :cfg="cfg" @toast="showToast" @cfg-changed="reloadCfg"/>
      </div>

      <!-- 分镜成片 -->
      <div v-show="page==='storyboard'">
        <StoryboardTab ref="storyboard" :cfg="cfg" @toast="showToast"/>
      </div>

      <!-- 图生视频 -->
      <div v-show="page==='remix'">
        <RemixTab ref="remix" :cfg="cfg" @toast="showToast"/>
      </div>
    </main>

    <div v-if="toast" :class="['toast', toastErr ? 'err' : '']">{{ toast }}</div>

    <!-- 风格画廊（上游 gallery 页面，全屏 iframe，跟风格库热更新走） -->
    <div v-if="showGallery" class="gallery-mask" @click.self="showGallery=false">
      <div class="gallery-box">
        <div class="gallery-bar">
          <b>手绘风格编号画廊</b>
          <span style="font-size:12px;color:var(--ink2)">点风格/版式/配色卡 = 看大图+复制提示词，<b style="color:var(--accent)">同时自动填入当前页签</b></span>
          <button class="btn" style="margin-left:auto" @click="showGallery=false">✕ 关闭</button>
        </div>
        <iframe ref="gframe" src="/gallery/index.html" class="gallery-frame" @load="onGalleryLoad"></iframe>
      </div>
    </div>

    <!-- 设置弹窗 -->
    <div v-if="showSettings" class="modal-mask" @click.self="showSettings=false">
      <div class="modal">
        <div class="mhead">
          <h3>设置</h3>
          <button class="btn" style="padding:2px 10px" @click="showSettings=false">✕</button>
        </div>
        <div class="mbody">
            <div class="field" style="margin-bottom:10px">
              <label>API Key 池（{{ settings.keys ? settings.keys.length : 0 }} 把 · 只显示尾 4 位，全文不外泄）</label>
              <div class="keyrow" v-for="k in (settings.keys || [])" :key="k.index">
                <span :class="['dot', keyStatOf(k.tail) ? 'bad' : '']" :title="keyStatOf(k.tail) ? '429 冷却中（剩 ' + keyStatOf(k.tail) + 's）' : '正常'"></span>
                <span class="keytail">{{ k.tail }}</span>
                <span v-if="keyStatOf(k.tail)" style="font-size:12px;color:var(--trap-ink)">冷却 {{ keyStatOf(k.tail) }}s</span>
                <button class="btn" style="font-size:12px;padding:2px 10px" @click="removeKey(k.index)">删除</button>
              </div>
            <div style="display:flex;gap:8px;margin-top:6px">
              <input type="text" v-model="newKey" placeholder="粘贴新的 API Key（sk-…）" style="flex:1">
              <button class="btn" @click="addKey">添加</button>
            </div>
          </div>
          <div class="fgrid">
            <div class="field wide"><label>接口地址 base_url（OpenAI 兼容即可：Agnes / 硅基流动 / OpenAI / 各类中转站）</label>
              <input type="text" v-model="setForm.base_url"></div>
            <div class="field"><label>生图模型名</label>
              <input type="text" v-model="setForm.model" list="modelhints">
              <datalist id="modelhints">
                <option value="agnes-image-2.1-flash"></option>
                <option value="agnes-image-2.5-flash"></option>
                <option value="Kolors"></option>
                <option value="stabilityai/stable-diffusion-3-5-large"></option>
                <option value="black-forest-labs/FLUX.1-schnell"></option>
                <option value="dall-e-3"></option>
              </datalist></div>
            <div class="field"><label>默认尺寸档位</label>
              <select v-model="setForm.size">
                <option v-for="s in imageSizes" :key="s" :value="s">{{ s }}</option>
                <option value="1024x768">1024x768（精确）</option>
                <option value="1024x1024">1024x1024（精确）</option>
              </select></div>
            <div class="field"><label>默认宽高比（档位尺寸用）</label>
              <select v-model="setForm.ratio">
                <option v-for="r in imageRatios" :key="r" :value="r">{{ r }}</option>
              </select></div>
            <div class="field"><label>超时（秒）</label><input type="number" v-model.number="setForm.timeout"></div>
            <div class="field"><label>失败重试次数</label><input type="number" v-model.number="setForm.retries"></div>
            <div class="field"><label>默认张数（1–6）</label><input type="number" v-model.number="setForm.default_count"></div>
            <div class="field"><label>素材保存根目录（留空 = exe 旁 out\）</label>
              <input type="text" v-model="setForm.out_dir" :placeholder="cfg ? cfg.out_dir : ''"></div>
            <div class="field"><label>视频模型</label>
              <input type="text" v-model="setForm.video_model" placeholder="agnes-video-2.5-flash"></div>
            <div class="field"><label>视频接口地址（留空 = 同生图 base_url）</label>
              <input type="text" v-model="setForm.video_base_url"></div>
            <div class="field wide"><label>文本/推理模型名（留空 = 不启用 AI 推荐和 SC-021 动态解析）</label>
              <div style="display:flex;gap:8px">
                <input type="text" v-model="setForm.text_model" list="textmodellist" style="flex:1" placeholder="点右侧「探测可用模型」从真实清单里选">
                <button class="btn" @click="probeModels" :disabled="probing">{{ probing ? '探测中…' : '探测可用模型' }}</button>
              </div>
              <datalist id="textmodellist">
                <option v-for="m in textModels" :key="m" :value="m"></option>
              </datalist>
              <div v-if="textModels.length" style="margin-top:6px">
                <select style="width:100%" :value="setForm.text_model" @change="setForm.text_model = $event.target.value">
                  <option value="">— 从探测到的 {{ textModels.length }} 个模型里选 —</option>
                  <option v-for="m in textModels" :key="m" :value="m">{{ m }}</option>
                </select>
              </div>
              <div v-if="probeMsg" style="font-size:12px;color:var(--ink2);margin-top:4px">{{ probeMsg }}</div>
            </div>
            <div class="field wide"><label>应用自更新源（托管 latest.json 的 URL，留空 = 不检查）</label>
              <input type="text" v-model="setForm.update_url" placeholder="https://example.com/update"></div>
          </div>
          <div class="mfoot">
            <span v-if="testMsg" :style="{color: testOk ? 'var(--ok-ink)' : 'var(--trap-ink)'}">{{ testMsg }}</span>
            <button class="btn" @click="testApi" style="margin-left:auto">测连通</button>
            <button class="btn primary" style="padding:6px 18px" @click="saveSettings">保存并生效</button>
          </div>

          <hr style="border:none;border-top:1px solid var(--line);margin:14px 0 10px">
          <div class="mfoot" style="margin:0">
            <b style="font-size:13px">风格库热更新</b>
            <span style="font-size:12px;color:var(--ink2)">本地 {{ libVer || '?' }} / 上游 {{ libUp || '?' }}</span>
            <button class="btn" @click="checkLib">查上游</button>
            <button class="btn" @click="syncLib">一键更新</button>
            <button class="btn" @click="checkAppUpdate">查应用更新</button>
          </div>
          <div v-if="upMsg" style="font-size:12px;color:var(--ink2);margin-top:8px;white-space:pre-wrap">{{ upMsg }}</div>
          <p class="mnote">换生图厂商：base_url + 模型名 + 对方 key，保存即生效。视频通道走同一套 key 池。</p>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { api, THEMES, IMAGE_SIZES, IMAGE_RATIOS } from './shared.js'
import StyleStrip from './components/StyleStrip.vue'
import SheetTab from './components/SheetTab.vue'
import CardTab from './components/CardTab.vue'
import LibraryTab from './components/LibraryTab.vue'
import StoryboardTab from './components/StoryboardTab.vue'
import RemixTab from './components/RemixTab.vue'

const PAGES = [
  { k: 'sheet', n: '设定表' },
  { k: 'free', n: '自由出图' },
  { k: 'card', n: '图文卡' },
  { k: 'library', n: '作品库' },
  { k: 'storyboard', n: '分镜成片' },
  { k: 'remix', n: '图生视频' },
]

export default {
  name: 'App',
  components: { StyleStrip, SheetTab, CardTab, LibraryTab, StoryboardTab, RemixTab },
  data() {
    return {
      PAGES, THEMES, page: 'sheet', theme: 'dark',
      version: '', libVer: '', libUp: '',
      cfg: null,
      styles: [], groups: [], colors: [], layouts: [],
      curNo: '', curStyle: null,
      showSettings: false, settings: {}, newKey: '', testMsg: '', testOk: false, upMsg: '',
      showGallery: false,
      setForm: { base_url:'', model:'', size:'1K', ratio:'4:3', timeout:360, retries:3,
        default_count:3, out_dir:'', video_model:'', video_base_url:'', update_url:'', text_model:'' },
      textModels: [], probing: false, probeMsg: '',
      keyStats: [],
      imageSizes: IMAGE_SIZES, imageRatios: IMAGE_RATIOS,
      toast: '', toastErr: false,
      _toastTimer: null,
    }
  },
  async mounted() {
    this.theme = (window.HD_THEME && window.HD_THEME.get()) || 'dark'
    // hash 直达页签：#page=library / storyboard / remix / card
    const m = (location.hash || '').match(/page=(\w+)/)
    if (m && PAGES.some(p => p.k === m[1])) this.page = m[1]
    await this.reloadCfg()
    const v = await api('GetAppVersion')
    this.version = v.version
    this.libVer = v.lib_version
  },
  methods: {
    async reloadCfg() {
      try {
        const [cfg, st, cols, lays] = await Promise.all([
          api('GetConfig'), api('GetStyles'), api('GetColors'), api('GetLayouts')])
        this.cfg = cfg
        this.styles = st.styles
        this.groups = st.groups
        this.colors = cols.colors
        this.layouts = lays.layouts
      } catch (e) { this.showToast(String(e.message || e), true) }
    },
    setTheme(k) { this.theme = k; window.HD_THEME && window.HD_THEME.set(k) },
    selectStyle(no) {
      if (no === this.curNo) { this.clearStyle(); return }   // 再点同一张 = 取消
      const st = this.styles.find(s => s.no === no)
      if (!st) return
      this.curNo = no
      this.curStyle = st
    },
    clearStyle() {
      this.curNo = ''
      this.curStyle = null
      this.showToast('已取消选中风格')
    },
    openImg(u) {
      api('OpenPath', this.outFileOf(u)).catch(() => { window.open(u, '_blank') })
    },
    // 在资源管理器中定位出图文件
    revealOut(u) {
      const p = this.outFileOf(u)
      api('RevealPath', p).catch(() => { this.showToast('打不开文件夹：' + p, true) })
    },
    // 复制出图文件完整路径到剪贴板
    async copyOut(u) {
      const p = this.outFileOf(u)
      try { await navigator.clipboard.writeText(p); this.showToast('路径已复制') }
      catch (e) {
        try { await api('CopyText', p); this.showToast('路径已复制') }
        catch (e2) { this.showToast('复制失败', true) }
      }
    },
    outFileOf(u) {
      if (!u.startsWith('/out/')) return u
      const cfgOut = (this.cfg && this.cfg.out_dir) || ''
      let name = u.slice(5)
      try { name = decodeURIComponent(name) } catch (e) {}
      return cfgOut.replace(/[\\/]+$/, '') + '\\' + name.split('/').join('\\')
    },
    /* ---------- 设置 ---------- */
    async openSettings() {
      try { this.settings = await api('GetSettings') } catch (e) { this.showToast(String(e.message || e), true); return }
      this.setForm = {
        base_url: this.settings.base_url || '',
        model: this.settings.model || '',
        size: this.settings.size || '1K',
        ratio: this.settings.ratio || '4:3',
        timeout: this.settings.timeout || 360,
        retries: this.settings.retries != null ? this.settings.retries : 3,
        default_count: this.settings.default_count || 3,
        out_dir: '',
        video_model: this.settings.video_model || '',
        video_base_url: this.settings.video_base_url || '',
        update_url: this.settings.update_url || '',
        text_model: this.settings.text_model || '',
      }
      this.testMsg = ''; this.upMsg = ''
      this.textModels = []; this.probeMsg = ''
      this.newKey = ''
      this.showSettings = true
      // key 冷却状态 + 文本模型可用性自动探测（打开弹窗即查，不用手点）
      api('KeyStatus').then(ks => { this.keyStats = ks || [] }).catch(() => {})
      this.probeModels(true)
    },
    keyStatOf(tail) {
      const s = (this.keyStats || []).find(k => k.key === tail)
      return s && s.cooling ? s.left : 0
    },
    async addKey() {
      if (!this.newKey.trim()) return
      const r = await api('AddKey', this.newKey.trim())
      if (r.error) { this.showToast(r.error, true); return }
      this.settings = r; this.newKey = ''
      this.reloadCfg()
      this.showToast('key 已添加并生效')
    },
    async removeKey(i) {
      const r = await api('RemoveKey', i)
      if (r.error) { this.showToast(r.error, true); return }
      this.settings = r
      this.reloadCfg()
      this.showToast('key 已删除')
    },
    async saveSettings() {
      const body = { ...this.setForm }
      if (!body.out_dir) delete body.out_dir   // 空 = 不改素材根
      const r = await api('SaveSettings', body)
      if (r.error) { this.showToast(r.error, true); return }
      let note = '设置已保存并生效'
      if (body.out_dir) {
        const d = await api('SetOutputDir', body.out_dir)
        note += d.error ? '；素材根设置失败：' + d.error : '；素材根已切换'
      }
      this.settings = r
      await this.reloadCfg()
      this.showToast(note)
    },
    async testApi() {
      this.testMsg = '测试中…'; this.testOk = false
      const r = await api('TestImageAPI')
      this.testOk = !!r.ok
      this.testMsg = r.msg || ''
    },
    async probeModels(silent) {
      silent = silent === true   // 防模板 @click 传事件对象误判
      this.probing = true
      if (!silent) { this.probeMsg = ''; this.textModels = [] }
      try {
        const r = await api('ListTextModels')
        if (r.error) { this.probeMsg = (silent ? '自动探测失败：' : '探测失败：') + r.error; return }
        this.textModels = r.models || []
        this.probeMsg = silent
          ? `自动探测到 ${this.textModels.length} 个模型（可重新手点探测刷新）`
          : `探测到 ${this.textModels.length} 个模型，从下方下拉选一个文本/推理模型`
      } catch (e) {
        this.probeMsg = (silent ? '自动探测失败：' : '探测失败：') + String(e.message || e)
      } finally {
        this.probing = false
      }
    },
    async checkLib() {
      this.upMsg = '查上游中…'
      const r = await api('LibCheckUpdate')
      if (r.error) { this.upMsg = r.error; return }
      this.libUp = r.upstream
      this.upMsg = r.update_available
        ? `有新版本：本地 ${r.local} → 上游 ${r.upstream}，点「一键更新」`
        : `已是最新（本地 ${r.local} = 上游 ${r.upstream}）`
    },
    async syncLib() {
      this.upMsg = '更新中：拉上游 zip → 覆盖 → 热重载…（约 10-40 秒）'
      const r = await api('LibSyncNow')
      if (r.error) { this.upMsg = r.error; return }
      const s = r.stats || {}
      this.libVer = r.version
      this.upMsg = `完成：覆盖 ${r.overwritten} 个文件，镜像清理 ${r.stale_deleted || 0} 个上游已删的旧文件（已备份），版本 ${r.version}，用时 ${r.elapsed}s` +
        (s.styles ? `\n风格 ${s.styles} / 陷阱 ${s.trap} / 配色 ${s.colors} / 版式 ${s.layouts}` : '')
      this.reloadCfg()
      this.showToast('风格库已热更新到 ' + r.version)
    },
    async checkAppUpdate() {
      this.upMsg = '检查应用更新中…'
      const r = await api('AppCheckUpdate')
      if (r.error) { this.upMsg = r.error; return }
      if (!r.update_available) {
        this.upMsg = (r.note ? r.note + '\n' : '') + `当前 ${r.current}，已是最新`
        return
      }
      if (confirm(`发现新版本 ${r.latest}（当前 ${r.current}）${r.note ? '\n' + r.note : ''}\n现在下载并重启更新吗？`)) {
        const u = await api('AppUpdateNow', r.url)
        this.upMsg = u.error ? u.error : (u.note || '更新中…')
      }
    },
    showToast(msg, isErr) {
      this.toast = msg
      this.toastErr = !!isErr
      clearTimeout(this._toastTimer)
      this._toastTimer = setTimeout(() => { this.toast = '' }, 2600)
    },
    /* ---------- 画廊一键填入（iframe 同源，点击卡片反向联动到页签） ---------- */
    onGalleryLoad() {
      try {
        const doc = this.$refs.gframe && this.$refs.gframe.contentDocument
        if (!doc || doc.__hdBound) return
        doc.__hdBound = true
        // 捕获阶段监听：画廊自己的 dialog 逻辑照常跑，我们只是额外把编号填进应用
        doc.addEventListener('click', (e) => {
          const card = e.target && e.target.closest ? e.target.closest('button[data-id], button[data-number]') : null
          if (!card) return
          this.applyGalleryPick(card)
        }, true)
        // iframe 内部切换子页面（index→layouts/colors）不触发外层 @load，轮询补绑
        const iv = setInterval(() => {
          if (!this.showGallery) { clearInterval(iv); return }
          const d2 = this.$refs.gframe && this.$refs.gframe.contentDocument
          if (d2 && !d2.__hdBound) this.onGalleryLoad()
        }, 1200)
      } catch (e) {}
    },
    applyGalleryPick(card) {
      const id = card.getAttribute('data-id') || card.getAttribute('data-number') || ''
      if (!id) return
      const cls = card.className || ''
      const name = card.getAttribute('data-name-zh') || card.getAttribute('data-name') || ''
      if (cls.includes('style-card')) {
        if (this.page === 'card' && this.$refs.card) {
          this.$refs.card.setStyle(id)
          this.showToast(`画廊选风格：# ${id} 已填入图文卡`)
        } else {
          this.selectStyle(id)
          this.showToast(`画廊选风格：# ${id} 已选中（设定表）`)
        }
        return
      }
      if (cls.includes('layout-card')) {
        if (this.$refs.card) this.$refs.card.setLayout(id)
        this.page = 'card'
        this.showToast(`画廊选版式：${id}${name ? ' ' + name : ''} 已填入图文卡`)
        return
      }
      if (cls.includes('color-card')) {
        if (this.page === 'card' && this.$refs.card) {
          this.$refs.card.setColor(id)
          this.showToast(`画廊选配色：${id} 已填入图文卡`)
        } else if (this.$refs.sheet) {
          this.$refs.sheet.f.color_id = id
          this.showToast(`画廊选配色：${id} 已填入设定表配色条`)
        }
      }
    },
  },
}
</script>
