<template>
  <div v-if="show" class="modal-mask" @click.self="$emit('close')">
    <div class="modal" style="width:860px">
      <div class="mhead"><h3>选配色（{{ colFiltered.length }} / {{ colors.length }} 套）</h3>
        <button class="btn" style="padding:2px 10px" @click="$emit('close')">✕</button></div>
      <div class="mbody">
        <div class="chiprow">
          <button :class="['chip', {on: !colCat}]" @click="colCat=''">全部 {{ colors.length }}</button>
          <button v-for="c in colCats" :key="c.key" :class="['chip', {on: colCat===c.key}]" @click="colCat = colCat===c.key ? '' : c.key">
            {{ c.zh }} {{ c.count }}
          </button>
        </div>
        <div class="pickgrid">
          <div :class="['pickcard', {on: !value}]" @click="$emit('pick', null)" style="display:flex;align-items:center;justify-content:center;color:var(--ink3)">
            <div class="pickno">不指定配色（自动搭配）</div>
          </div>
          <div v-for="c in colFiltered" :key="c.id" :class="['pickcard', {on: value===c.id}]" @click="$emit('pick', c)">
            <img v-if="c.img" :src="'/lib-img/' + encodeURIComponent(c.img)" loading="lazy" alt="">
            <div class="pickno">{{ c.id }} {{ c.name }}</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script>
// 共享配色选择弹窗（分类筛选 chips + 缩略图网格），CardTab / SheetTab 共用
export default {
  name: 'ColorPickerModal',
  props: { show: Boolean, colors: Array, value: String },
  emits: ['pick', 'close'],
  data() { return { colCat: '' } },
  computed: {
    colCats() {
      const m = new Map()
      for (const c of this.colors) {
        const key = c.cat || '其他'
        if (!m.has(key)) m.set(key, { key, zh: c.cat_zh || key, count: 0 })
        m.get(key).count++
      }
      return [...m.values()].sort((a, b) => b.count - a.count)
    },
    colFiltered() {
      return this.colors.filter(c => !this.colCat || (c.cat || '其他') === this.colCat)
    },
  },
  watch: { show(v) { if (v) this.colCat = '' } },
}
</script>
