<template>
  <div>
    <div class="twocol">
    <div class="colform">
    <div class="charbar">
      <button class="btn" @click="showStyles = true">选风格·可选（{{ styleNo || '自动' }}）</button>
      <button class="btn" @click="showLayouts = true">选版式·可选（{{ f.layout_id || '自动' }}）</button>
      <button class="btn" @click="showColors = true">选配色·可选（{{ colorLabel }}）</button>
      <button class="btn" :disabled="!anyTheme || recommending" @click="aiRecommend" title="按主题让文本模型推荐风格编号+配色，确认后填入">
        {{ recommending ? '推荐中…' : 'AI 推荐' }}
      </button>
      <button v-if="f.layout_id === 'SC-021'" class="btn" :disabled="!anyTheme || previewing" @click="scPreview" title="文本模型按上游规则预解析本次生成将使用的执行提示词（不消耗轮替次数）">
        {{ previewing ? '解析中…' : 'SC-021 解析预览' }}
      </button>
      <span v-if="f.layout_id === 'SC-021' && scState" class="scprog" title="五大玩法严格轮替，生成成功才推进">下次玩法 {{ scState.next }}/{{ scState.total }} · {{ scState.name }}</span>
    </div>
    <div v-if="recMsg" class="recmsg">{{ recMsg }}
      <template v-if="pendingRec">
        <span style="display:inline-flex;gap:8px;margin-left:10px">
          <button class="btn primary" style="padding:2px 14px" @click="applyRec">应用</button>
          <button class="btn" style="padding:2px 10px" @click="dropRec">不用</button>
        </span>
      </template>
    </div>
    <div v-if="batchMsg" class="recmsg">{{ batchMsg }}</div>
    <div v-if="scMsg" class="recmsg"><b>本次将轮替到玩法{{ scMsg.playstyle }}（{{ scMsg.playstyle_name }}）</b><br>{{ scMsg.prompt }}</div>

    <div class="fgrid">
      <div class="field wide"><label>主题（图文卡必填{{ batchMode ? '；批量模式下一行一个主题' : '' }}）</label>
        <textarea v-model="f.card_theme" :rows="batchMode ? 5 : 2" :placeholder="batchMode ? '一行一个主题，每行生成一张卡，例：凌晨四点的早市' : '例：凌晨四点的早市 / 反内耗的一天'"></textarea>
        <div class="themebar" v-if="themeFavs.length || themeHist.length">
          <span v-for="t in themeFavs" :key="'f'+t" class="themefav" @click="fillTheme(t)" title="点击填入">★ {{ t }}</span>
          <select v-if="themeHist.length" class="themehist" @change="fillTheme($event.target.value); $event.target.selectedIndex = 0">
            <option value="">最近主题…</option>
            <option v-for="t in themeHist" :key="t" :value="t">{{ t.length > 24 ? t.slice(0,24) + '…' : t }}</option>
          </select>
          <a v-if="!batchMode && batchThemes.length === 1" class="themefavbtn" @click="toggleFav" title="加入/移出收藏">{{ themeFavs.includes(batchThemes[0]) ? '★ 已收藏' : '☆ 收藏本主题' }}</a>
        </div>
      </div>
      <div class="field wide"><label>风格补充特征（留空 = 用库里的正向特征）</label>
        <input type="text" v-model="f.style_feature"></div>
      <div class="field wide"><label style="display:inline-flex;align-items:center;gap:6px;cursor:pointer">
        <input type="checkbox" v-model="batchMode" style="width:auto"> 批量模式（多主题排队，一张一张出）
      </label></div>
    </div>

    <div class="genrow">
      <button class="btn primary" :disabled="!canGen || polling" @click="generate">
        {{ polling ? '生成中…' : (batchThemes.length > 1 ? '批量生成 ' + batchThemes.length + ' 张' : '生成图文卡') }}
      </button>
      <SizePicker :f="f"/>
    </div>

    </div>

    <!-- 右栏：预览 + 结果 -->
    <div class="colside">
    <div class="pvblock" style="margin-top:0">
      <div class="pvhead"><h3>提示词预览</h3>
        <div class="pvtools">
          <button class="btn" @click="copyPrompt" style="font-size:12px;padding:4px 12px">复制</button>
        </div>
      </div>
      <pre class="prompt">{{ prompt || '（写主题即可生成；版式/配色/风格都可不选，未选的由生图模型自动搭配）' }}</pre>
      <div v-if="jobLog.length" class="genlog"><div v-for="(l,i) in jobLog" :key="i">{{ l }}</div></div>
      <div v-if="lastErr" class="recmsg" style="color:var(--trap-ink)">失败：{{ lastErr }}
        <button class="btn" style="padding:2px 12px;margin-left:10px" @click="generate">重试</button>
      </div>
      <div v-if="results.length" class="results">
        <div class="resbar"><span>本次 {{ results.length }} 张 · 提示词 <a @click="copyPrompt">复制</a></span></div>
        <figure v-for="(u,i) in results" :key="i">
          <img :src="u" @click="$parent.openImg(u)" alt="">
          <figcaption>
            <a @click="$parent.openImg(u)">大图</a>
            <a @click="$parent.revealOut(u)">文件夹</a>
            <a @click="$parent.copyOut(u)">复制路径</a>
          </figcaption>
        </figure>
      </div>
    </div>
    </div><!-- /colside -->
    </div><!-- /twocol -->

    <!-- 版式选择弹窗 -->
    <div v-if="showLayouts" class="modal-mask" @click.self="showLayouts=false">
      <div class="modal" style="width:860px">
        <div class="mhead"><h3>选版式（{{ layFiltered.length }} / {{ layouts.length }} 款）</h3>
          <button class="btn" style="padding:2px 10px" @click="showLayouts=false">✕</button></div>
        <div class="mbody">
          <input type="text" v-model="layQ" placeholder="搜版式名 / 编号 / 关键词" style="width:100%;margin-bottom:8px">
          <div class="chiprow">
            <button :class="['chip', {on: !layCat}]" @click="layCat=''">全部 {{ layouts.length }}</button>
            <button v-for="c in layCats" :key="c.key" :class="['chip', {on: layCat===c.key}]" @click="layCat = layCat===c.key ? '' : c.key">
              {{ c.zh }} {{ c.count }}
            </button>
          </div>
          <div class="pickgrid">
            <div :class="['pickcard', {on: !f.layout_id}]" @click="pickLayout(null)" style="display:flex;align-items:center;justify-content:center;color:var(--ink3)">
              <div class="pickno">不指定版式（自由排版）</div>
            </div>
            <div v-for="l in layFiltered" :key="l.id" :class="['pickcard', {on: f.layout_id===l.id}]" @click="pickLayout(l)">
              <img v-if="l.img" :src="libImg(l.img)" loading="lazy" alt="">
              <div class="pickno">{{ l.id }} {{ l.name }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 配色选择弹窗（共享组件） -->
    <ColorPickerModal :show="showColors" :colors="colors" :value="f.color_id"
      @pick="pickColor" @close="showColors=false"/>

    <!-- 风格选择弹窗（可选） -->
    <div v-if="showStyles" class="modal-mask" @click.self="showStyles=false">
      <div class="modal" style="width:860px">
        <div class="mhead"><h3>选风格（{{ styleFiltered.length }} / {{ styles.length }} 款）</h3>
          <button class="btn" style="padding:2px 10px" @click="showStyles=false">✕</button></div>
        <div class="mbody">
          <input type="text" v-model="styleQ" placeholder="搜编号 / 风格名 / 画风参考" style="width:100%;margin-bottom:8px">
          <div class="chiprow">
            <button :class="['chip', {on: !styleGroup}]" @click="styleGroup=''">全部分组</button>
            <button v-for="g in styleGroups" :key="g.key" :class="['chip', {on: styleGroup===g.key}]" @click="styleGroup = styleGroup===g.key ? '' : g.key">
              {{ g.key }} {{ g.count }}
            </button>
            <label class="chk" style="margin-left:auto"><input type="checkbox" v-model="styleHideTrap"> 隐藏陷阱</label>
          </div>
          <div class="pickgrid">
            <div :class="['pickcard', {on: !styleNo}]" @click="pickStyle(null)" style="display:flex;align-items:center;justify-content:center;color:var(--ink3)">
              <div class="pickno">不指定风格（自动）</div>
            </div>
            <div v-for="s in styleFiltered" :key="s.no" :class="['pickcard', {on: styleNo===s.no}]" @click="pickStyle(s)">
              <img v-if="s.img" :src="'/img/' + s.no" loading="lazy" alt="">
              <div class="pickno">#{{ s.no }} {{ shortStyle(s) }}</div>
              <span v-if="s.trap" class="trapmark">陷阱</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { api } from '../shared.js'
import SizePicker from './SizePicker.vue'
import ColorPickerModal from './ColorPickerModal.vue'

const CARD_FORM_KEY = 'hd_card_form_v1'
const CARD_THEME_KEY = 'hd_card_themes_v1'
const CARD_FAV_KEY = 'hd_card_favs_v1'

export default {
  name: 'CardTab',
  components: { SizePicker, ColorPickerModal },
  props: { cfg: Object, styles: Array, colors: Array, layouts: Array },
  emits: ['toast'],
  data() {
    return {
      f: { card_theme: '', style_feature: '', layout_id: '', color_id: '', count: 1, size: '1K', ratio: '4:3' },
      styleNo: '', showStyles: false, styleQ: '', styleGroup: '', styleHideTrap: false,
      showLayouts: false, showColors: false, layQ: '', layCat: '',
      batchMode: false,
      themeHist: [], themeFavs: [],
      scState: null,
      recommending: false, recMsg: '', pendingRec: null, _waitTimer: null,
      previewing: false, scMsg: null,
      prompt: '', polling: false, jobId: '', jobLog: [], results: [], lastErr: '',
      batchTotal: 0, batchMsg: '',
      _pvTimer: null, _pollTimer: null,
    }
  },
  computed: {
    anyTheme() { return !!this.batchThemes.length },
    batchThemes() {
      if (!this.batchMode) {
        const t = this.f.card_theme.trim()
        return t ? [t] : []
      }
      return this.f.card_theme.split('\n').map(s => s.trim()).filter(Boolean)
    },
    canGen() { return this.anyTheme && !this.polling },
    colorLabel() {
      if (!this.f.color_id) return '自动'
      const c = (this.colors || []).find(x => x.id === this.f.color_id)
      return c ? this.f.color_id + ' ' + c.name : this.f.color_id
    },
    layCats() {
      // 参考上游图文画廊：按 category 分类（social-card/infographic/comic-storyboard/ip-character/ecommerce）
      const zhMap = {
        'social-card': '社媒卡', 'infographic': '信息图', 'comic-storyboard': '漫画分镜',
        'ip-character': 'IP角色', 'ecommerce': '电商',
      }
      const m = new Map()
      for (const l of this.layouts) {
        const key = l.cat || '其他'
        if (!m.has(key)) m.set(key, { key, zh: zhMap[key] || key, count: 0 })
        m.get(key).count++
      }
      return [...m.values()].sort((a, b) => b.count - a.count)
    },
    layFiltered() {
      const q = this.layQ.trim().toLowerCase()
      return this.layouts.filter(l => {
        if (this.layCat && (l.cat || '其他') !== this.layCat) return false
        if (!q) return true
        return (l.id + ' ' + l.name + ' ' + l.name_en + ' ' + (l.keywords || []).join(' ')).toLowerCase().includes(q)
      })
    },
    styleGroups() {
      const m = new Map()
      for (const s of this.styles) {
        const key = s.group || '未分组'
        if (!m.has(key)) m.set(key, { key, count: 0 })
        m.get(key).count++
      }
      return [...m.values()].sort((a, b) => a.key < b.key ? -1 : 1)
    },
    styleFiltered() {
      const q = this.styleQ.trim().toLowerCase()
      return this.styles.filter(s => {
        if (this.styleHideTrap && s.trap) return false
        if (this.styleGroup && (s.group || '未分组') !== this.styleGroup) return false
        if (!q) return true
        return (s.no + ' ' + s.gen + ' ' + s.ref).toLowerCase().includes(q)
      })
    },
  },
  mounted() {
    this.restoreForm()
    this.loadThemes()
    if (this.cfg) {
      if (this.cfg.size) this.f.size = this.cfg.size
      if (this.cfg.ratio) this.f.ratio = this.cfg.ratio
    }
    this.schedulePreview()
    this.refreshScState()
  },
  methods: {
    libImg(p) {
      // 本地绝对路径 → 转成 /lib-img/ 由后端伺服
      return '/lib-img/' + encodeURIComponent(p)
    },
    /* ---- 持久化 ---- */
    saveForm() {
      try {
        localStorage.setItem(CARD_FORM_KEY, JSON.stringify({ f: this.f, styleNo: this.styleNo, batchMode: this.batchMode }))
      } catch (e) {}
    },
    saveFormSoon() {
      clearTimeout(this._saveTimer)
      this._saveTimer = setTimeout(() => this.saveForm(), 400)
    },
    restoreForm() {
      let d = null
      try { d = JSON.parse(localStorage.getItem(CARD_FORM_KEY) || 'null') } catch (e) {}
      if (!d) return
      if (d.f) Object.keys(this.f).forEach(k => { if (d.f[k] !== undefined && d.f[k] !== null) this.f[k] = d.f[k] })
      if (d.styleNo) this.styleNo = d.styleNo
      this.batchMode = !!d.batchMode
    },
    /* ---- 主题历史 / 收藏 ---- */
    loadThemes() {
      try { this.themeHist = JSON.parse(localStorage.getItem(CARD_THEME_KEY) || '[]') } catch (e) { this.themeHist = [] }
      try { this.themeFavs = JSON.parse(localStorage.getItem(CARD_FAV_KEY) || '[]') } catch (e) { this.themeFavs = [] }
    },
    rememberTheme(t) {
      this.themeHist = [t, ...this.themeHist.filter(x => x !== t)].slice(0, 20)
      try { localStorage.setItem(CARD_THEME_KEY, JSON.stringify(this.themeHist)) } catch (e) {}
    },
    fillTheme(t) {
      if (!t) return
      if (this.batchMode) {
        // 批量模式下追加一行；单主题模式直接替换
        this.f.card_theme = (this.f.card_theme.trim() ? this.f.card_theme.replace(/\s+$/, '') + '\n' : '') + t
      } else {
        this.f.card_theme = t
      }
    },
    toggleFav() {
      const t = this.batchThemes[0]
      if (!t) return
      if (this.themeFavs.includes(t)) this.themeFavs = this.themeFavs.filter(x => x !== t)
      else this.themeFavs = [t, ...this.themeFavs].slice(0, 12)
      try { localStorage.setItem(CARD_FAV_KEY, JSON.stringify(this.themeFavs)) } catch (e) {}
    },
    /* ---- SC-021 进度 ---- */
    async refreshScState() {
      try { this.scState = await api('Sc021State') } catch (e) {}
    },
    /* ---- 画廊联动入口 ---- */
    setLayout(id) { this.f.layout_id = id || ''; this.saveForm(); this.schedulePreview() },
    setColor(id) { this.f.color_id = id || ''; this.saveForm(); this.schedulePreview() },
    setStyle(no) { this.styleNo = no || ''; this.saveForm(); this.schedulePreview() },
    pickLayout(l) { this.f.layout_id = l ? l.id : ''; this.showLayouts = false; this.saveForm() },
    pickColor(c) { this.f.color_id = c ? c.id : ''; this.showColors = false; this.saveForm() },
    pickStyle(s) {
      this.styleNo = s ? (this.styleNo === s.no ? '' : s.no) : ''
      if (this.styleNo) this.showStyles = false
      this.saveForm()
    },
    shortStyle(s) {
      const g = s.gen || s.ref || ''
      return g.length > 10 ? g.slice(0, 10) + '…' : g
    },
    schedulePreview() {
      clearTimeout(this._pvTimer)
      this._pvTimer = setTimeout(() => this.refreshPreview(), 350)
    },
    payloadFor(theme) {
      return {
        mode: 'card', style_no: this.styleNo,
        card_theme: theme, style_feature: this.f.style_feature,
        layout_id: this.f.layout_id, color_id: this.f.color_id,
        count: this.f.count, size: this.f.size, ratio: this.f.ratio,
      }
    },
    payload() { return this.payloadFor(this.batchThemes[0] || this.f.card_theme.trim()) },
    async copyPrompt() {
      if (!this.prompt) return
      try { await navigator.clipboard.writeText(this.prompt); this.$emit('toast', '提示词已复制') }
      catch (e) { this.$emit('toast', '复制失败', true) }
    },
    async aiRecommend() {
      if (!this.f.card_theme.trim()) { this.$emit('toast', '先写主题再点 AI 推荐', true); return }
      this.recommending = true; this.recMsg = this.waitMsg(); this.pendingRec = null
      this._waitTimer = setInterval(() => { if (this.recommending) this.recMsg = this.waitMsg() }, 15000)
      try {
        const r = await api('CardRecommend', { card_theme: this.f.card_theme })
        if (r.error) { this.recMsg = ''; this.$emit('toast', r.error, true); return }
        // 不直接填入：先给确认条（应用/不用）
        const parts = []
        parts.push(r.style_no ? `风格 #${r.style_no}${r.style_name ? ' ' + r.style_name : ''}` : '风格不指定')
        parts.push(r.color_id ? `配色 ${r.color_id}${r.color_name ? ' ' + r.color_name : ''}` : '配色不指定')
        this.pendingRec = r
        this.recMsg = `AI 推荐：${parts.join(' · ')}。理由：${r.reason || '（模型没给理由）'}（${Math.round(r.elapsed)}s）`
      } catch (e) {
        this.recMsg = ''
        this.$emit('toast', String(e.message || e), true)
      } finally {
        clearInterval(this._waitTimer)
        this.recommending = false
      }
    },
    waitMsg() {
      this._waitTick = (this._waitTick || 0) + 1
      const msgs = ['已提交，模型思考中（约 30–60 秒）…', '还在思考：正在比对风格意境与配色…', '快好了，正在解析返回结果…']
      return '文本模型' + msgs[Math.min((this._waitTick - 1) % 3, 2)]
    },
    applyRec() {
      const r = this.pendingRec
      if (!r) return
      if (r.style_no) this.styleNo = r.style_no
      if (r.color_id) this.f.color_id = r.color_id
      this.pendingRec = null
      this.recMsg = ''
      this.saveForm()
      this.$emit('toast', 'AI 推荐已应用')
    },
    dropRec() { this.pendingRec = null; this.recMsg = '' },
    async scPreview() {
      if (!this.f.card_theme.trim()) { this.$emit('toast', '先写主题', true); return }
      this.previewing = true; this.scMsg = null
      try {
        const r = await api('Sc021Preview', { card_theme: this.f.card_theme, style_no: this.styleNo })
        if (r.error) { this.$emit('toast', r.error, true); return }
        this.scMsg = r
        this.refreshScState()
        this.$emit('toast', `解析完成：玩法${r.playstyle}，${Math.round(r.elapsed)}s`)
      } catch (e) {
        this.$emit('toast', String(e.message || e), true)
      } finally {
        this.previewing = false
      }
    },
    async refreshPreview() {
      if (!this.f.card_theme.trim() && !this.f.layout_id && !this.f.color_id) { this.prompt = ''; return }
      try {
        const r = await api('BuildPrompt', this.payload())
        if (r.error) { this.$emit('toast', r.error, true); return }
        this.prompt = r.prompt
      } catch (e) {}
    },
    async generate() {
      const themes = this.batchThemes
      if (!themes.length) { this.$emit('toast', '图文卡要先写主题', true); return }
      if (this.polling) return
      if (themes.length === 1) {
        await this.generateOne(themes[0])
        return
      }
      // 批量模式：一张一张排队出
      this.lastErr = ''
      for (let i = 0; i < themes.length; i++) {
        this.batchMsg = `批量进行中：第 ${i + 1} / ${themes.length} 张（${themes[i]}）`
        this.jobLog = [...this.jobLog, `—— 批量 ${i + 1}/${themes.length}：${themes[i]} ——`]
        const ok = await this.generateOne(themes[i], true)
        if (!ok) { this.batchMsg = ''; this.$emit('toast', `批量第 ${i + 1} 张失败，已停止`, true); return }
      }
      this.batchMsg = ''
      this.$emit('toast', `批量完成：${themes.length} 张`)
    },
    // 单张生成并等待完成；silent=true 时不弹 toast（批量用），返回是否成功
    generateOne(theme, silent) {
      return new Promise(async (resolve) => {
        this.rememberTheme(theme)
        let r
        try { r = await api('Generate', this.payloadFor(theme)) } catch (e) {
          this.lastErr = String(e.message || e); this.$emit('toast', this.lastErr, true); resolve(false); return
        }
        if (r.error) { this.lastErr = r.error; this.$emit('toast', r.error, true); resolve(false); return }
        this.jobId = r.job_id
        if (!silent) { this.jobLog = []; this.results = [] }
        this.polling = true
        const origDone = this._onDone, origErr = this._onErr
        this._onDone = () => {
          if (!silent) this.$emit('toast', '图文卡完成')
          resolve(true); this._onDone = origDone; this._onErr = origErr
        }
        this._onErr = (err) => {
          if (!silent) this.$emit('toast', '失败：' + (err || ''), true)
          resolve(false); this._onDone = origDone; this._onErr = origErr
        }
        this.poll()
      })
    },
    async poll() {
      clearTimeout(this._pollTimer)
      if (!this.jobId) { this.polling = false; return }
      let r
      try { r = await api('GetJob', this.jobId) } catch (e) { r = null }
      if (r && r.ok) {
        this.jobLog = r.log || []
        if (r.status === 'done') {
          this.polling = false
          this.results = r.urls || []
          this.lastErr = ''
          if (this._onDone) { this._onDone(this.results); this._onDone = null }
          else this.$emit('toast', '图文卡完成')
          this.refreshScState()
          return
        }
        if (r.status === 'error') {
          this.polling = false
          this.lastErr = r.error || ''
          if (this._onErr) { this._onErr(r.error); this._onErr = null }
          else this.$emit('toast', '失败：' + this.lastErr, true)
          return
        }
      }
      this._pollTimer = setTimeout(() => this.poll(), 1500)
    },
  },
  watch: {
    f: { handler() { this.saveFormSoon(); this.schedulePreview() }, deep: true },
    styleNo() { this.schedulePreview() },
    batchMode() { this.saveFormSoon() },
  },
}
</script>
