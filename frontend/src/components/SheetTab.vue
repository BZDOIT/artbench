<template>
  <div class="twocol">
    <div class="colform">

    <!-- 自由出图 -->
    <div v-if="mode==='free'">
      <div class="field" style="margin-bottom:10px">
        <label>提示词（想画什么写什么；上面的风格条可选——点了风格卡就带风格，再点取消就是纯自由生图）</label>
        <textarea v-model="f.free_text" rows="7"
          placeholder="例：一个戴草帽的老农在田埂上骑二八自行车，夕阳，胶片感"></textarea>
      </div>
      <div class="genrow">
        <button class="btn primary" :disabled="!canGen || polling" @click="generate">
          {{ polling ? '生成中…' : '开始出图' }}
        </button>
        <SizePicker :f="f"/>
      </div>
    </div>

    <!-- 设定表 -->
    <div v-else>
      <div class="charbar">
        <select v-model="loadedChar" @change="applyChar">
          <option value="">— 角色档案 —</option>
          <option v-for="c in characters" :key="c.char_name" :value="c.char_name">{{ c.char_name }}</option>
        </select>
        <button class="btn" @click="saveChar">存为档案</button>
        <button class="btn" @click="delChar">删除</button>
      </div>

      <div class="fgrid">
        <div class="field"><label>角色名</label><input type="text" v-model="f.char_name" placeholder="如：王小明"></div>
        <div class="field"><label>身份</label><input type="text" v-model="f.identity" placeholder="如：山村小卖部老板"></div>
        <div class="field"><label>年龄形态</label><input type="text" v-model="f.age_form" placeholder="如：35岁大叔"></div>
        <div class="field"><label>性格</label><input type="text" v-model="f.personality" placeholder="如：憨厚、爱唠叨"></div>
        <div class="field"><label>世界观题材</label><input type="text" v-model="f.world" placeholder="如：现代乡村"></div>
        <div class="field"><label>背景色</label><input type="text" v-model="f.bg" placeholder="默认米白色"></div>
        <div class="field wide"><label>人设标语</label><input type="text" v-model="f.slogan"></div>
        <div class="field wide"><label>风格补充特征（留空 = 用库里的正向特征）</label>
          <textarea v-model="f.style_feature"></textarea></div>
        <div class="field"><label>转面视角（、分隔）</label><textarea v-model="f.views" placeholder="留空用默认 5 视角"></textarea></div>
        <div class="field"><label>表情（、分隔）</label><textarea v-model="f.expressions" placeholder="留空用默认 7 表情"></textarea></div>
        <div class="field"><label>动作姿势（、分隔）</label><textarea v-model="f.actions"></textarea></div>
        <div class="field"><label>细节特写（、分隔）</label><textarea v-model="f.details"></textarea></div>
        <div class="field"><label>身高（cm，留空不出对比）</label><input type="text" v-model="f.height"></div>
        <div class="field"><label>对比参照身高</label><input type="text" v-model="f.ref_height" placeholder="默认 180"></div>
        <div class="field wide"><label>一致性锚点（可选）</label><input type="text" v-model="f.consistency"></div>
        <div class="field"><label>模式</label>
          <select v-model="f.mode">
            <option value="full">完整（档案/转面/表情/动作/细节/体型）</option>
            <option value="lite">精简（档案/细节）</option>
          </select>
        </div>
        <div class="field"><label>配色条（可选）</label>
          <button class="btn" style="width:100%" @click="showColors = true">
            {{ colorLabel }}
          </button>
        </div>
        <div class="field wide"><label>配色条自定义文本</label><input type="text" v-model="f.palette"></div>
        <div class="field wide"><label>额外要求（拼词尾部，不占编号）</label><input type="text" v-model="f.extra"></div>
        <div class="field wide"><label>负向词（留空用默认防乱码尾巴）</label><input type="text" v-model="f.negatives"></div>
      </div>

      <div class="genrow">
        <button class="btn primary" :disabled="!canGen || polling" @click="generate">
          {{ polling ? '生成中…' : '开始出图' }}
        </button>
        <SizePicker :f="f"/>
      </div>
    </div>
    </div><!-- /colform -->

    <!-- 右栏：预览 + 结果 + 历史 -->
    <div class="colside">
    <div class="pvblock">
      <div class="pvhead"><h3>提示词预览</h3>
        <div class="pvtools">
          <button class="btn" @click="copyPrompt" style="font-size:12px;padding:4px 12px">复制</button>
        </div>
      </div>
      <div v-if="trapError" class="trapwarn">{{ trapError }}</div>
      <pre class="prompt">{{ prompt || '（选一个风格编号，填内容后自动生成提示词）' }}</pre>

      <div v-if="jobLog.length" class="genlog"><div v-for="(l,i) in jobLog" :key="i">{{ l }}</div></div>

      <div v-if="lastErr" class="recmsg" style="color:var(--trap-ink)">失败：{{ lastErr }}
        <button class="btn" style="padding:2px 12px;margin-left:10px" @click="generate">重试</button>
      </div>

      <div v-if="results.length" class="results">
        <figure v-for="(u,i) in results" :key="i">
          <img :src="u" @click="$parent.openImg(u)" alt="">
          <figcaption>
            <a @click="$parent.openImg(u)">大图</a>
            <a @click="$parent.revealOut(u)">文件夹</a>
            <a @click="$parent.copyOut(u)">复制路径</a>
          </figcaption>
        </figure>
      </div>

      <div class="histhead"><h3>出图历史</h3>
        <select v-model="histMode" style="margin-left:auto;font-size:12px;padding:3px 6px">
          <option value="">全部类型</option>
          <option value="sheet">设定表</option>
          <option value="free">自由出图</option>
          <option value="card">图文卡</option>
        </select>
        <input type="text" v-model="histQ" placeholder="过滤：编号 / 角色名 / 提示词" style="font-size:12px;padding:3px 8px;width:180px">
      </div>
      <div class="hist">
        <div v-for="h in historyFiltered" :key="h.id" :class="['hitem','st-'+h.status]" @click="expanded = expanded===h.id ? 0 : h.id">
          <div class="hmeta">
            <span>#{{ h.id }}</span><span>{{ h.created }}</span>
            <span>{{ modeLabel(h.mode) }}</span>
            <span>风格 #{{ h.style_no || '—' }}</span>
            <span>{{ h.char_name || '未命名' }}</span>
            <span :style="{color: stColor(h.status)}">{{ h.status }}{{ h.elapsed ? ' · ' + h.elapsed + 's' : '' }}</span>
            <a v-if="h.status==='done'" style="color:var(--accent)" @click.stop="delHist(h)">删除</a>
          </div>
          <div class="hprompt">{{ h.prompt }}</div>
          <div v-if="expanded===h.id && h.urls && h.urls.length" class="himgs">
            <img v-for="(u,i) in h.urls" :key="i" :src="u" @click.stop="$parent.openImg(u)" alt="">
          </div>
          <div v-if="expanded===h.id && h.error" class="hprompt" style="color:var(--trap-ink)">{{ h.error }}</div>
        </div>
        <div v-if="!historyFiltered.length" class="gempty">{{ histQ || histMode ? '没有匹配的记录' : '还没有出图记录' }}</div>
      </div>
    </div>
    </div><!-- /colside -->

    <!-- 配色条选择弹窗（共享组件） -->
    <ColorPickerModal :show="showColors" :colors="colors" :value="f.color_id"
      @pick="c => { f.color_id = c ? c.id : ''; showColors = false; saveForm() }"
      @close="showColors = false"/>
  </div><!-- /twocol -->
</template>

<script>
import { api, IMAGE_SIZES, IMAGE_RATIOS } from '../shared.js'
import SizePicker from './SizePicker.vue'
import ColorPickerModal from './ColorPickerModal.vue'

const FORM_KEY = 'hd_form_v1'
const FORM_IDS = ['char_name','identity','age_form','personality','world','slogan','style_feature',
  'bg','mode','views','expressions','actions','details','height','ref_height','consistency',
  'palette','color_id','extra','negatives','size','count','ratio','free_text']

export default {
  name: 'SheetTab',
  components: { SizePicker, ColorPickerModal },
  props: { cfg: Object, styles: Array, colors: Array, curNo: String, curStyle: Object, pageMode: String },
  emits: ['toast', 'style-cleared'],
  data() {
    return {
      mode: 'sheet',
      characters: [],
      f: {
        char_name:'', identity:'', age_form:'', personality:'', world:'', slogan:'', style_feature:'',
        bg:'米白色', mode:'full', views:'', expressions:'', actions:'', details:'',
        height:'', ref_height:'', consistency:'', palette:'', color_id:'', extra:'', negatives:'',
        size:'1K', ratio:'4:3', count:3, free_text:'',
      },
      prompt: '', polling: false, jobId: '', jobLog: [], results: [], lastErr: '',
      history: [], expanded: 0, loadedChar: '', trapError: '', histQ: '', histMode: '',
      showColors: false,
      _saveTimer: null, _pvTimer: null, _pollTimer: null,
    }
  },
  computed: {
    canGen() {
      if (this.mode === 'free') return !!this.f.free_text.trim()
      return !!this.curStyle
    },
    colorLabel() {
      if (!this.f.color_id) return '不用配色条（点击选择）'
      const c = (this.colors || []).find(x => x.id === this.f.color_id)
      return c ? '配色条：' + this.f.color_id + ' ' + c.name : '配色条：' + this.f.color_id
    },
    historyFiltered() {
      const q = this.histQ.trim().toLowerCase()
      return this.history.filter(h => {
        if (this.histMode && this.modeGroup(h.mode) !== this.histMode) return false
        if (!q) return true
        return ((h.style_no || '') + ' ' + (h.char_name || '') + ' ' + (h.prompt || '') + ' ' + (h.created || ''))
          .toLowerCase().includes(q)
      })
    },
  },
  async mounted() {
    this.restoreForm()
    this.characters = await api('GetCharacters')
    this.refreshHist()
    this.schedulePreview()
  },
  methods: {
    stColor(st) {
      return st === 'done' ? 'var(--ok-ink)' : st === 'error' ? 'var(--trap-ink)' : 'var(--run-ink)'
    },
    // 后端 mode 原始值：full/lite=设定表，free=自由，card=图文卡，''=旧记录归设定表
    modeGroup(m) {
      if (m === 'card' || m === 'free') return m
      return 'sheet'
    },
    modeLabel(m) {
      const g = this.modeGroup(m)
      return g === 'card' ? '[图文卡]' : g === 'free' ? '[自由]' : '[设定表]'
    },
    saveForm() {
      const d = {}
      FORM_IDS.forEach(k => { d[k] = this.f[k] })
      d._style = this.curNo
      d._mode = this.mode
      try { localStorage.setItem(FORM_KEY, JSON.stringify(d)) } catch (e) {}
    },
    saveFormSoon() {
      clearTimeout(this._saveTimer)
      this._saveTimer = setTimeout(() => this.saveForm(), 400)
      this.schedulePreview()
    },
    restoreForm() {
      let d = null
      try { d = JSON.parse(localStorage.getItem(FORM_KEY) || 'null') } catch (e) {}
      if (!d) return
      FORM_IDS.forEach(k => { if (d[k] !== undefined && d[k] !== null) this.f[k] = d[k] })
      if (d._mode) this.mode = d._mode
    },
    applyChar() {
      if (!this.loadedChar) return
      const c = this.characters.find(x => x.char_name === this.loadedChar)
      if (!c) return
      FORM_IDS.forEach(k => {
        if (c[k] !== undefined && c[k] !== null && !['size','count','mode','ratio'].includes(k)) this.f[k] = c[k]
      })
      this.saveForm(); this.schedulePreview()
    },
    schedulePreview() {
      clearTimeout(this._pvTimer)
      this._pvTimer = setTimeout(() => this.refreshPreview(), 350)
    },
    async refreshPreview() {
      if (this.mode === 'free') {
        if (!this.f.free_text.trim() && !this.curStyle) { this.prompt = ''; this.trapError = ''; return }
      } else if (!this.curStyle) {
        this.prompt = ''; this.trapError = ''; return
      }
      try {
        const r = await api('BuildPrompt', this.payload())
        if (r.error) { this.$emit('toast', r.error, true); return }
        this.prompt = r.prompt
        this.trapError = ''
      } catch (e) {}
    },
    payload() {
      const p = {}
      FORM_IDS.forEach(k => { p[k] = this.f[k] })
      p.style_no = this.curNo
      if (this.mode === 'free') p.mode = 'free'
      return p
    },
    copyPrompt() {
      if (!this.prompt) return
      navigator.clipboard.writeText(this.prompt).then(
        () => this.$emit('toast', '已复制'),
        () => this.$emit('toast', '复制失败', true))
    },
    async generate() {
      if (this.mode === 'free') {
        if (!this.f.free_text.trim()) { this.$emit('toast', '先写一句提示词', true); return }
      } else if (!this.curStyle) {
        this.$emit('toast', '先选一个风格编号', true); return
      }
      this.trapError = ''
      this.lastErr = ''
      let r
      try { r = await api('Generate', this.payload()) } catch (e) { this.$emit('toast', String(e.message || e), true); return }
      if (r.error) {
        if (r.trap) this.trapError = r.error
        this.$emit('toast', r.error, true)
        return
      }
      this.jobId = r.job_id
      this.jobLog = []
      this.results = []
      this.polling = true
      this.poll()
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
          this.$emit('toast', '出图完成，' + (r.elapsed || 0) + ' 秒')
          this.refreshHist()
          return
        }
        if (r.status === 'error') {
          this.polling = false
          this.lastErr = r.error || ''
          this.$emit('toast', '出图失败：' + this.lastErr, true)
          this.refreshHist()
          return
        }
      }
      this._pollTimer = setTimeout(() => this.poll(), 1500)
    },
    async refreshHist() {
      try {
        const r = await api('ListHistory', 60)
        this.history = r.history || []
      } catch (e) {}
    },
    async delHist(h) {
      const r = await api('DeleteHistory', h.id)
      this.history = r.history || []
      this.$emit('toast', '已删除记录 #' + h.id + '（图片文件还在磁盘上）')
    },
    async saveChar() {
      if (!this.f.char_name.trim()) { this.$emit('toast', '先填角色名再存档案', true); return }
      const r = await api('SaveCharacter', this.payload())
      if (r.error) { this.$emit('toast', r.error, true); return }
      this.characters = r.characters
      this.loadedChar = this.f.char_name
      this.$emit('toast', '已存档案：' + this.f.char_name)
    },
    async delChar() {
      if (!this.loadedChar) return
      const r = await api('DeleteCharacter', this.loadedChar)
      this.characters = r.characters
      this.loadedChar = ''
      this.$emit('toast', '已删除档案')
    },
  },
  watch: {
    f: { handler() { this.saveFormSoon() }, deep: true },
    curNo() { this.schedulePreview() },
    // 顶级页签（设定表/自由出图）驱动内部模式切换
    pageMode: {
      handler(v) {
        if ((v === 'free' || v === 'sheet') && v !== this.mode) {
          this.mode = v
          this.saveForm()
          this.schedulePreview()
        }
      },
      immediate: true,
    },
  },
}
</script>
