<template>
  <section class="gallery">
    <div class="gtools">
      <input type="text" v-model="q" placeholder="搜编号 / 风格名 / 画风参考">
      <select v-model="groupSel">
        <option value="">全部分组</option>
        <option v-for="g in groups" :key="g" :value="g">{{ g }}</option>
      </select>
      <label class="chk"> <input type="checkbox" v-model="hideTrap"> 隐藏陷阱</label>
      <span class="gmeta">{{ filtered.length }} / {{ styles.length }} 风格 · 点卡片选中，再点取消</span>
    </div>
    <div class="gwrap">
      <div class="ggrid">
        <div v-for="s in filtered" :key="s.no" :class="['scard', {on: curNo===s.no}]" @click="$emit('select', s.no)">
          <img :src="imgUrl(s)" loading="lazy" alt="">
          <div class="no">#{{ s.no }} {{ short(s) }}</div>
          <span v-if="s.trap" class="trapmark">陷阱</span>
        </div>
        <div v-if="!filtered.length" class="gempty">没有匹配的风格</div>
      </div>
    </div>
  </section>
</template>

<script>
export default {
  name: 'StyleStrip',
  props: { styles: Array, groups: Array, curNo: String },
  emits: ['select'],
  data() { return { q: '', groupSel: '', hideTrap: false } },
  computed: {
    filtered() {
      const q = this.q.trim().toLowerCase()
      return this.styles.filter(s => {
        if (this.hideTrap && s.trap) return false
        if (this.groupSel && (s.group || '未分组') !== this.groupSel) return false
        if (!q) return true
        return (s.no + ' ' + s.gen + ' ' + s.ref).toLowerCase().includes(q)
      })
    },
  },
  methods: {
    short(s) {
      const g = s.gen || s.ref || ''
      return g.length > 10 ? g.slice(0, 10) + '…' : g
    },
    imgUrl(s) {
      return s.img ? '/img/' + s.no : (s.sheet ? '/sheet/' + s.sheet : '')
    },
  },
}
</script>
