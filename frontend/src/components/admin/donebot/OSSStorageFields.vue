<template>
  <div class="grid gap-4 sm:grid-cols-2">
    <p class="text-sm text-gray-500 sm:col-span-2">服务器上传可走加速节点；读取、删除和用户签名下载走普通地域节点。请先在 OSS 控制台启用桶的传输加速。</p>
    <label class="block text-sm"><span class="mb-1 block">上传 Endpoint（支持传输加速）</span><input v-model.trim="model.endpoint" type="url" class="input" placeholder="https://oss-accelerate.aliyuncs.com" :disabled="disabled" :data-testid="`${scope}-oss-upload-endpoint`"><span class="mt-1 block text-xs text-gray-500">不含 Bucket 名称。也可填写桶所在地域的普通 Endpoint。</span></label>
    <label class="block text-sm"><span class="mb-1 block">下载 Endpoint（普通地域节点）</span><input v-model.trim="model.download_endpoint" type="url" class="input" placeholder="https://oss-cn-hangzhou.aliyuncs.com" :disabled="disabled" :data-testid="`${scope}-oss-download-endpoint`"><span class="mt-1 block text-xs text-gray-500">必须对应桶所在地域，不含 Bucket 名称，不使用加速节点。</span></label>
    <label class="block text-sm"><span class="mb-1 block">Region（桶所在地域）</span><input v-model.trim="model.region" class="input" placeholder="cn-hangzhou" :disabled="disabled" :data-testid="`${scope}-oss-region`"></label>
    <label class="block text-sm"><span class="mb-1 block">Bucket</span><input v-model.trim="model.bucket" class="input" :disabled="disabled" :data-testid="`${scope}-oss-bucket`"></label>
    <label class="block text-sm"><span class="mb-1 block">对象前缀</span><input v-model.trim="model.prefix" class="input" placeholder="generated/" :disabled="disabled" :data-testid="`${scope}-oss-prefix`"></label>
    <label class="block text-sm"><span class="mb-1 block">Access Key ID</span><input v-model.trim="model.access_key_id" class="input" autocomplete="off" :disabled="disabled" :data-testid="`${scope}-oss-access-key`"></label>
    <label class="block text-sm sm:col-span-2"><span class="mb-1 block">Secret Access Key</span><input v-model="model.secret_access_key" type="password" class="input" autocomplete="new-password" :placeholder="model.secret_configured ? '已配置，留空保留原值' : '填写 OSS Secret Access Key'" :disabled="disabled" :data-testid="`${scope}-oss-secret`"></label>
    <p class="text-xs text-gray-500 sm:col-span-2">默认使用私有桶，网关生成限时签名链接，客户端无需存储密钥。网关密钥仅需指定前缀的读、写、删除权限，不需要桶管理权限。<template v-if="scope === 'release'">安装包长期保留，不受媒体到期清理影响。</template><template v-else>到期后由网关定时清理对象，不是仅使链接失效；生成产物桶请勿启用版本控制或阻止删除的保留策略，以免保留旧版本。</template></p>
  </div>
</template>
<script setup lang="ts">
import type { ObjectStorageSettings } from '@/api/admin/donebot'
const model = defineModel<ObjectStorageSettings>({ required: true })
defineProps<{ scope: string; disabled: boolean }>()
</script>
