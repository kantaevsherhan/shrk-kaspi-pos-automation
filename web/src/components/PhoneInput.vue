<script setup>
import { ElInput } from 'element-plus';
import { formatNationalPhone, nationalPhone } from '../lib/phone';

defineProps({ modelValue: { type: String, default: '' } });
const emit = defineEmits(['update:modelValue', 'blur']);
</script>

<template>
  <el-input
    :model-value="modelValue"
    :formatter="formatNationalPhone"
    :parser="nationalPhone"
    type="tel"
    inputmode="tel"
    autocomplete="tel-national"
    placeholder="(707) 123-45-67"
    maxlength="32"
    @update:model-value="emit('update:modelValue', $event)"
    @blur="emit('blur', $event)"
  >
    <template #prepend>+7</template>
  </el-input>
</template>
