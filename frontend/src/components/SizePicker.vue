<template>
  <span class="sizepicker">
    <span class="field" style="display:inline-block;width:90px">
      <label>尺寸档位</label>
      <select v-model="f.size">
        <option v-for="s in sizes" :key="s" :value="s">{{ s }}</option>
      </select>
    </span>
    <span class="field" style="display:inline-block;width:100px">
      <label>宽高比</label>
      <select v-model="f.ratio">
        <option v-for="r in ratios" :key="r" :value="r">{{ r }}</option>
      </select>
    </span>
    <span class="field" style="display:inline-block;width:90px">
      <label>张数</label>
      <select v-model.number="f.count">
        <option v-for="n in 6" :key="n" :value="n">{{ n }}</option>
      </select>
    </span>
  </span>
</template>

<script>
import { IMAGE_SIZES, IMAGE_RATIOS } from '../shared.js'
export default {
  name: 'SizePicker',
  props: { f: Object },
  data() { return { sizes: IMAGE_SIZES, ratios: IMAGE_RATIOS } },
}
</script>
