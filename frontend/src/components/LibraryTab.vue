<template>
  <div class="libwrap">
    <div class="gtools">
      <select v-model="kind" @change="refresh">
        <option value="all">全部</option>
        <option value="img">图片</option>
        <option value="video">视频</option>
      </select>
      <button class="btn" :class="{on: multi}" @click="toggleMulti">{{ multi ? '退出多选' : '多选删除' }}</button>
      <button v-if="multi" class="btn" @click="selAll">全选</button>
      <button v-if="multi" class="btn" @click="selNone">全不选</button>
      <button v-if="multi" class="btn danger" :disabled="!selSet.size" @click="delSel">
        删除选中（{{ selSet.size }}）
      </button>
      <span class="gmeta">{{ items.length }} 个素材 · 根目录 {{ outDir }}</span>
    </div>
    <div class="libgrid">
      <div v-for="it in items" :key="it.file" :class="['libitem', {sel: selSet.has(it.file)}]"
        @click="multi ? toggleSel(it.file) : preview = it">
        <img v-if="it.kind==='img'" :src="it.url" loading="lazy" alt="">
        <video v-else :src="it.url" preload="metadata"></video>
        <div class="libname" :title="it.name">{{ it.name }}</div>
        <span v-if="multi && selSet.has(it.file)" class="chkmark">✓</span>
      </div>
      <div v-if="!items.length" class="gempty">素材目录还是空的（出图/出视频后自动出现在这里）</div>
    </div>

    <!-- 预览 -->
    <div v-if="preview" class="modal-mask" @click.self="preview=null">
      <div class="modal" style="width:auto;max-width:90vw;background:transparent;border:none">
        <img v-if="preview.kind==='img'" :src="preview.url" style="max-width:86vw;max-height:80vh;border-radius:10px">
        <video v-else :src="preview.url" controls autoplay style="max-width:86vw;max-height:80vh;border-radius:10px"></video>
        <div style="text-align:center;margin-top:8px">
          <button class="btn" @click="openOne(preview.file)">用系统程序打开</button>
          <button class="btn" @click="revealOne(preview.file)">在文件夹中定位</button>
          <button class="btn danger" @click="delOne(preview)">删除（进回收站）</button>
          <button class="btn" @click="preview=null">关闭</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
import { api } from '../shared.js'
export default {
  name: 'LibraryTab',
  props: { cfg: Object },
  emits: ['toast', 'cfg-changed'],
  data() { return { items: [], kind: 'all', multi: false, selSet: new Set(), preview: null, outDir: '' } },
  mounted() { this.refresh() },
  methods: {
    async refresh() {
      try {
        const r = await api('ScanLibrary', this.kind, 500)
        this.items = r.items || []
        this.outDir = r.out_dir || ''
      } catch (e) { this.$emit('toast', String(e.message || e), true) }
    },
    toggleMulti() {
      this.multi = !this.multi
      this.selSet = new Set()
    },
    toggleSel(f) {
      const s = new Set(this.selSet)
      if (s.has(f)) s.delete(f); else s.add(f)
      this.selSet = s
    },
    selAll() { this.selSet = new Set(this.items.map(i => i.file)) },
    selNone() { this.selSet = new Set() },
    async delSel() {
      if (!confirm('把选中的 ' + this.selSet.size + ' 个文件删到回收站？（可还原）')) return
      const r = await api('DeleteFiles', Array.from(this.selSet))
      if (r.error) { this.$emit('toast', r.error, true); return }
      if (!r.deleted) {
        this.$emit('toast', '删除失败：' + (r.failed || []).join('、'), true)
        return
      }
      this.selSet = new Set()
      const failNote = (r.failed && r.failed.length) ? '；失败 ' + r.failed.length + ' 个：' + r.failed.join('、') : ''
      this.$emit('toast', '已删除 ' + r.deleted + ' 个（回收站可还原）' + failNote, !!failNote)
      this.refresh()
    },
    async delOne(it) {
      if (!confirm('把 ' + it.name + ' 删到回收站？')) return
      const r = await api('DeleteFiles', [it.file])
      if (r.error || !r.deleted) {
        this.$emit('toast', '删除失败：' + (r.error || (r.failed || []).join('、') || '文件可能被占用'), true)
        return
      }
      this.preview = null
      this.$emit('toast', '已删除（回收站可还原）')
      this.refresh()
    },
    openOne(f) { api('OpenPath', f) },
    revealOne(f) { api('RevealPath', f) },
  },
}
</script>
