<template>
  <div class="sbwrap">
    <div class="twocol">
    <div class="colform">
    <!-- 成片设定 -->
    <div class="charbar">
      <input type="text" v-model="film.title" placeholder="片名" style="width:180px">
      <select v-model="film.mode">
        <option value="dual">双关键帧（衔接顺滑）</option>
        <option value="cut">硬切</option>
      </select>
      <select v-model="film.aspect">
        <option v-for="a in aspects" :key="a" :value="a">{{ a }} · {{ hint[a] || '' }}</option>
      </select>
      <select v-model="film.seconds">
        <option v-for="n in [4,5,6,7,8,10,12]" :key="n" :value="n">{{ n }} 秒/镜</option>
      </select>
      <button class="btn" @click="save">保存设定</button>
      <button class="btn" @click="resume">继续未完成</button>
    </div>
    </div><!-- /colform -->

    <!-- 右栏：镜头列表 -->
    <div class="colside">
    <!-- 镜头列表 -->
    <div class="shotlist">
      <div v-for="(s, i) in shots" :key="s.id" class="shotcard" :class="'st-'+s.status">
        <div class="shotmedia">
          <img v-if="s.img_ok" :src="s.img">
          <label v-else class="upload" style="width:120px;height:68px">
            + 首帧
            <input type="file" accept="image/*" style="display:none" @change="e => upload(e, s, 'img')">
          </label>
          <button v-if="s.img_ok" class="btn" style="font-size:11px;padding:1px 6px" @click="clearImg(s, 'img')">换</button>
        </div>
        <div class="shotbody">
          <div class="shotmeta">
            <b>#{{ String(s.seq).padStart(2, '0') }}</b>
            <select v-model="s.camera" style="width:70px">
              <option value="">运镜</option><option v-for="c in cameras" :key="c" :value="c">{{ c }}</option>
            </select>
            <select v-model="s.seconds" style="width:64px">
              <option v-for="n in [4,5,6,7,8,10,12]" :key="n" :value="n">{{ n }}s</option>
            </select>
            <span :style="{color: stColor(s.status)}">{{ stText(s) }}</span>
            <span v-if="s.prog && s.prog.secs" style="color:var(--ink3)">{{ s.prog.secs }}s</span>
          </div>
          <textarea v-model="s.action" rows="2" placeholder="这一镜演什么（如：主角推门进屋，回头看一眼）"></textarea>
          <div class="shotops">
            <button class="btn" style="font-size:12px" @click="genOne(s)">生成</button>
            <button class="btn" style="font-size:12px" @click="retryOne(s)">重抽</button>
            <button class="btn" style="font-size:12px" @click="delShot(s)">删</button>
            <label class="btn" style="font-size:12px;padding:3px 8px">尾帧
              <input type="file" accept="image/*" style="display:none" @change="e => upload(e, s, 'img2')">
            </label>
            <span v-if="s.last_from" style="font-size:11px;color:var(--ink3)">尾帧：{{ s.last_from }}</span>
          </div>
          <video v-if="s.result && s.status==='done'" :src="s.result" controls style="width:100%;margin-top:6px"></video>
          <div v-if="s.error" style="font-size:12px;color:var(--trap-ink)">{{ s.error }}</div>
        </div>
      </div>
      <button class="btn" style="margin:8px 0" @click="addShot">＋ 加一镜</button>
    </div>
    </div><!-- /colside -->
    </div><!-- /twocol -->

    <!-- 拼接 -->
    <div class="charbar" style="margin-top:12px">
      <button class="btn primary" :disabled="!doneCount" @click="concat">拼接成片（勾选 {{ doneCount }} 镜完成）</button>
      <video v-if="film.movie" :src="film.movie" controls style="width:320px;max-height:180px"></video>
    </div>
  </div>
</template>

<script>
import { api, ASPECTS, ASPECT_HINT } from '../shared.js'
export default {
  name: 'StoryboardTab',
  props: { cfg: Object },
  emits: ['toast'],
  data() {
    return {
      film: { title: '', mode: 'dual', seconds: 5, aspect: '16:9', movie: '' },
      shots: [],
      aspects: ASPECTS, hint: ASPECT_HINT,
      cameras: ['推', '拉', '摇', '移', '跟拍', '固定', '环绕'],
      _timer: null,
    }
  },
  computed: {
    doneCount() { return this.shots.filter(s => s.status === 'done' && s.result).length },
  },
  async mounted() { await this.refresh(); this.pollLoop() },
  unmounted() { clearTimeout(this._timer) },
  methods: {
    stColor(st) {
      return st === 'done' ? 'var(--ok-ink)' : st === 'error' ? 'var(--trap-ink)' : st === 'pending' ? 'var(--ink3)' : 'var(--run-ink)'
    },
    stText(s) {
      if (s.prog && s.prog.stage && (s.status === 'running' || s.status === 'queued')) return s.prog.stage
      const m = { pending: '待生成', queued: '排队中', running: '生成中', done: '完成', error: '失败' }
      return m[s.status] || s.status
    },
    async refresh() {
      try {
        const r = await api('GetFilm')
        if (r.film && r.film.id) {
          this.film = { ...this.film, ...r.film }
          this.shots = r.shots || []
        } else {
          this.shots = []
        }
      } catch (e) {}
    },
    pollLoop() {
      // 间隔在 setTimeout 调用时就求值，running 必须在这里取（不能用回调里的局部变量）
      const running = this.shots.some(s => s.status === 'queued' || s.status === 'running')
      this._timer = setTimeout(async () => {
        if (running) await this.refresh()
        this.pollLoop()
      }, running ? 2500 : 5000)
    },
    async save() {
      const r = await api('SaveFilm', { ...this.film, shots: this.shots })
      this.film = { ...this.film, ...r.film }
      this.shots = r.shots || []
      this.$emit('toast', '已保存')
    },
    addShot() {
      this.shots.push({
        id: 0, seq: this.shots.length + 1, img: '', img2: '', action: '', camera: '',
        seconds: this.film.seconds, prompt: '', status: 'pending', video_id: '', result: '',
        error: '', prog: {}, img_ok: false, img2_ok: false, last_from: '',
      })
      this.save()
    },
    async upload(e, s, field) {
      const file = e.target.files[0]
      if (!file) return
      const b64 = await new Promise(res => {
        const rd = new FileReader()
        rd.onload = () => res(rd.result)
        rd.readAsDataURL(file)
      })
      const r = await api('UploadShotImg', b64)
      if (r.error) { this.$emit('toast', r.error, true); return }
      s[field] = r.url
      await this.save()
    },
    async clearImg(s, field) {
      s[field] = ''
      await this.save()
    },
    async genOne(s) {
      await this.save()
      const r = await api('GenerateShots', [s.id])
      this.$emit('toast', '已入队 ' + r.queued + ' 镜')
      this.refresh()
    },
    async retryOne(s) {
      await this.save()
      await api('RetryShot', s.id)
      this.refresh()
    },
    async delShot(s) {
      if (!confirm('删除第 ' + s.seq + ' 镜？')) return
      const r = await api('DeleteShot', s.id)
      this.film = { ...this.film, ...r.film }
      this.shots = r.shots || []
    },
    async resume() {
      const r = await api('ResumePending')
      this.$emit('toast', '已恢复：续轮询 ' + r.resume_poll + ' 个，重建 ' + r.requeue + ' 个')
      this.refresh()
    },
    async concat() {
      const ids = this.shots.filter(s => s.status === 'done' && s.result).map(s => s.id)
      const r = await api('ConcatShots', ids)
      if (r.error) { this.$emit('toast', r.error, true); return }
      this.$emit('toast', '拼接完成：' + r.count + ' 段，' + r.mb + ' MB，用时 ' + r.seconds + 's（' + r.mode + '）')
      this.refresh()
    },
  },
}
</script>
