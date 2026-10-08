<template>
  <div class="sbwrap">
    <div class="twocol">
    <div class="colform">
    <div class="fgrid">
      <div class="field"><label>标题（可选）</label><input type="text" v-model="f.title"></div>
      <div class="field"><label>模式</label>
        <select v-model="f.mode">
          <option value="reference">垫图 + 提示词</option>
          <option value="text">纯提示词（不用图）</option>
        </select>
      </div>
      <div class="field"><label>时长（4–12 秒）</label>
        <select v-model.number="f.seconds">
          <option v-for="n in [4,5,6,7,8,10,12]" :key="n" :value="n">{{ n }} 秒</option>
        </select>
      </div>
      <div class="field"><label>画幅比例</label>
        <select v-model="f.aspect">
          <option v-for="a in aspects" :key="a" :value="a">{{ a }} · {{ hint[a] || '' }}</option>
        </select>
      </div>
      <div class="field wide" style="position:relative">
        <label>提示词（打 @ 从参考图里点选插入，如 @图1 @图2，发送时自动翻译成接口写法）</label>
        <textarea ref="pt" v-model="f.prompt" rows="4" @input="onPromptInput" @keyup="onPromptInput" @click="onPromptInput"
          placeholder="例：先点下方＋上传参考图，再打 @ 点选插入，如 @图1 里的人物走进 @图2 的场景"></textarea>
        <div v-if="atOpen" class="atpick">
          <div v-if="!refs.length" class="atempty">还没有参考图——先点下方 ＋ 上传</div>
          <div v-for="(r, i) in refs" :key="i" class="atitem" @mousedown.prevent="pickRef(i)">
            <img :src="r"><span>@图{{ i + 1 }}</span>
          </div>
        </div>
      </div>
      <div class="field wide">
        <label style="display:flex;align-items:center;gap:6px;cursor:pointer">
          <input type="checkbox" v-model="f.redraw">
          改图转画风（把照片转手绘/动漫风格时勾选，自动附加「只提取特征从零重绘，不要在照片上加质感」指令）
        </label>
      </div>
      <div class="field wide">
        <label>参考图（{{ refs.length }}/{{ refMax }} 张，点图可插入 @图N；垫图模式下必填）</label>
        <div class="refrow">
          <div v-for="(r, i) in refs" :key="i" class="refthumb">
            <img :src="r" title="点击在提示词光标处插入 @图N" @click="insertToken(i)">
            <span class="reftag">@图{{ i + 1 }}</span>
            <button class="refdel" @click="refs.splice(i, 1)">✕</button>
          </div>
          <label v-if="refs.length < refMax" class="refadd">＋<input type="file" accept="image/*" multiple style="display:none" @change="addRefs"></label>
        </div>
      </div>
    </div>
    <div class="genrow">
      <button class="btn primary" :disabled="polling" @click="generate">{{ polling ? '已入队…' : '生成视频' }}</button>
    </div>
    </div><!-- /colform -->

    <!-- 右栏：片段列表 -->
    <div class="colside">
    <div class="histhead"><h3>片段列表</h3></div>
    <div class="hist">
      <div v-for="c in clips" :key="c.id" :class="['hitem', 'st-' + c.status]">
        <div class="hmeta">
          <span>#{{ c.id }}</span><span>{{ c.title }}</span>
          <span>{{ c.seconds }}s · {{ c.aspect }}</span>
          <span>{{ c.refs ? c.refs.length : 0 }} 图</span>
          <span :style="{color: stColor(c.status)}">
            {{ c.prog && c.prog.stage && (c.status==='running'||c.status==='queued') ? c.prog.stage : c.status }}
          </span>
          <a v-if="c.result" style="color:var(--accent)" @click="playClip(c)">播放</a>
          <a style="color:var(--accent)" @click="redoClip(c)">重做</a>
          <a style="color:var(--trap-ink)" @click="delClip(c)">删除</a>
        </div>
        <div class="hprompt">{{ c.prompt }}</div>
        <div v-if="c.refs && c.refs.length" class="himgs">
          <img v-for="(u, i) in c.refs" :key="i" :src="u">
        </div>
        <div v-if="playingId === c.id" style="margin-top:6px">
          <video :src="c.result" controls autoplay style="width:100%;max-height:320px"></video>
        </div>
        <div v-if="c.error" style="font-size:12px;color:var(--trap-ink)">{{ c.error }}</div>
      </div>
      <div v-if="!clips.length" class="gempty">还没有片段</div>
    </div>
    </div><!-- /colside -->
    </div><!-- /twocol -->
  </div>
</template>

<script>
import { api, ASPECTS, ASPECT_HINT } from '../shared.js'

const REMIX_FORM_KEY = 'hd_remix_form_v1'

export default {
  name: 'RemixTab',
  props: { cfg: Object },
  emits: ['toast'],
    data() {
    return {
      f: { title: '', mode: 'reference', seconds: 5, aspect: '16:9', prompt: '', redraw: false },
      refs: [], refMax: 5, clips: [],
      aspects: ASPECTS, hint: ASPECT_HINT,
      playingId: 0, _timer: null,
      atOpen: false, atPos: -1,
    }
  },
  async mounted() {
    this.restoreForm()
    await this.refresh()
    this.pollLoop()
  },
  unmounted() { clearTimeout(this._timer) },
  methods: {
    saveForm() {
      try { localStorage.setItem(REMIX_FORM_KEY, JSON.stringify(this.f)) } catch (e) {}
    },
    restoreForm() {
      let d = null
      try { d = JSON.parse(localStorage.getItem(REMIX_FORM_KEY) || 'null') } catch (e) {}
      if (d) Object.keys(this.f).forEach(k => { if (d[k] !== undefined && d[k] !== null) this.f[k] = d[k] })
    },
    stColor(st) {
      return st === 'done' ? 'var(--ok-ink)' : st === 'error' ? 'var(--trap-ink)' : st === 'pending' ? 'var(--ink3)' : 'var(--run-ink)'
    },
    async refresh() {
      try {
        const r = await api('GetClips')
        this.clips = r.clips || []
        if (r.defaults) this.refMax = r.defaults.max_refs || 5
      } catch (e) {}
    },
    pollLoop() {
      this._timer = setTimeout(async () => {
        const busy = this.clips.some(c => c.status === 'queued' || c.status === 'running')
        if (busy) await this.refresh()
        this.pollLoop()
      }, this.clips.some(c => c.status === 'queued' || c.status === 'running') ? 2500 : 6000)
    },
    // 打 @ 弹出参考图选图浮层
    onPromptInput() {
      const el = this.$refs.pt
      if (!el) return
      const pos = el.selectionStart
      if (pos > 0 && this.f.prompt[pos - 1] === '@') {
        this.atPos = pos - 1
        this.atOpen = true
      } else {
        this.atOpen = false
      }
    },
    hidePick() { this.atOpen = false },
    // 把光标处的 "@" 替换为 "@图N "（或直接在光标处插入）
    pickRef(i) { this.insertToken(i) },
    insertToken(i) {
      const el = this.$refs.pt
      const token = '@图' + (i + 1) + ' '
      let v = this.f.prompt
      let pos = el ? el.selectionStart : v.length
      if (this.atOpen && this.atPos >= 0 && v[this.atPos] === '@') {
        v = v.slice(0, this.atPos) + token + v.slice(this.atPos + 1)
        pos = this.atPos + token.length
      } else {
        v = v.slice(0, pos) + token + v.slice(pos)
        pos += token.length
      }
      this.f.prompt = v
      this.atOpen = false
      this.$nextTick(() => {
        if (el) { el.focus(); el.setSelectionRange(pos, pos) }
      })
    },
    async addRefs(e) {
      for (const file of e.target.files) {
        if (this.refs.length >= this.refMax) break
        const b64 = await new Promise(res => {
          const rd = new FileReader()
          rd.onload = () => res(rd.result)
          rd.readAsDataURL(file)
        })
        // 存临时文件：UploadShotImg 复用（data/shot_imgs），返回 /shots-img/ URL
        const r = await api('UploadShotImg', b64)
        if (r.error) { this.$emit('toast', r.error, true); break }
        this.refs.push(r.url)
      }
      e.target.value = ''
    },
    // 上游改图转绘指令（handdraw-style-prompter SKILL.md）：垫图改画风时强制重绘，杜绝滤镜叠加
    redrawSuffix() {
      return '【请只提取参考图人物的五官特征和姿态，场景轮廓，在画风上严格按提示词描述的风格重新画，不要在参考照片上加质感。】'
    },
    payload() {
      const p = { ...this.f, refs: this.refs.slice() }
      if (this.f.redraw && this.f.mode === 'reference') {
        p.prompt = this.f.prompt.trim() + '\n' + this.redrawSuffix
      }
      return p
    },
    async generate() {
      if (this.f.mode === 'reference' && !this.refs.length) { this.$emit('toast', '垫图模式至少要 1 张参考图', true); return }
      if (!this.f.prompt.trim()) { this.$emit('toast', '先写提示词', true); return }
      const r = await api('GenerateClip', this.payload())
      if (r.error) { this.$emit('toast', r.error, true); return }
      this.$emit('toast', '片段 #' + r.id + ' 已入队')
      this.saveForm()
      this.refresh()
    },
    async redoClip(c) {
      const r = await api('SaveClip', {
        id: c.id, title: c.title, prompt: c.prompt, mode: c.mode,
        seconds: c.seconds, aspect: c.aspect, refs: c.refs,
      })
      if (r.error) { this.$emit('toast', r.error, true); return }
      const g = await api('GenerateClip', {
        id: c.id, title: c.title, prompt: c.prompt, mode: c.mode,
        seconds: c.seconds, aspect: c.aspect, refs: c.refs,
      })
      if (g.error) { this.$emit('toast', g.error, true); return }
      this.$emit('toast', '片段 #' + c.id + ' 重新入队')
      this.refresh()
    },
    async delClip(c) {
      if (!confirm('删除片段 #' + c.id + '？')) return
      await api('DeleteClipID', c.id)
      this.refresh()
    },
    playClip(c) { this.playingId = this.playingId === c.id ? 0 : c.id },
  },
  watch: { f: { handler() { this.saveForm() }, deep: true } },
}
</script>
