<template>
 <section class="space-y-3"><div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">安装记录</h2><button class="btn btn-secondary" :disabled="busy" @click="load">刷新</button></div><p class="text-sm text-gray-500">安装由用户登录后领取。吊销立即停用该安装凭据。</p>
 <div class="overflow-x-auto rounded-xl border border-gray-200 dark:border-gray-700"><table class="w-full min-w-[640px] text-left text-sm"><thead class="bg-gray-50 dark:bg-dark-800"><tr><th class="p-3">安装</th><th class="p-3">用户 / 凭据编号</th><th class="p-3">状态</th><th class="p-3">最后在线</th><th class="p-3">操作</th></tr></thead><tbody><tr v-for="item in items" :key="item.id" class="border-t border-gray-100 dark:border-gray-700"><td class="p-3">{{ item.name }}</td><td class="p-3">{{ item.user_id }} / {{ item.api_key_id }}</td><td class="p-3">{{ item.status }}</td><td class="p-3">{{ item.last_seen_at ? new Date(item.last_seen_at).toLocaleString() : '尚未使用' }}</td><td class="p-3"><button class="btn btn-danger" :disabled="busy || item.status!=='active'" @click="revoke(item)">吊销</button></td></tr><tr v-if="!items.length"><td colspan="5" class="p-6 text-center text-gray-500">暂无安装</td></tr></tbody></table></div>
 <div class="flex items-center justify-end gap-3 text-sm"><button class="btn btn-secondary" :disabled="busy || page<=1" @click="page--;load()">上一页</button><span>{{ page }} / {{ Math.max(1,Math.ceil(total/20)) }}</span><button class="btn btn-secondary" :disabled="busy || page*20>=total" @click="page++;load()">下一页</button></div>
 </section>
</template>
<script setup lang="ts">
import { ref,onMounted } from 'vue'
import donebot,{ type Install } from '@/api/admin/donebot'
import { useAppStore } from '@/stores/app'
const items=ref<Install[]>([]),total=ref(0),page=ref(1),busy=ref(false),app=useAppStore()
async function load(){busy.value=true;try{const result=await donebot.installs(page.value);items.value=result.items;total.value=result.total}catch(e){app.showError(e instanceof Error?e.message:'加载失败')}finally{busy.value=false}}
async function revoke(item:Install){if(!window.confirm(`确定吊销安装“${item.name}”？`))return;busy.value=true;try{await donebot.revoke(item.id);app.showSuccess('安装已吊销');await load()}catch(e){app.showError(e instanceof Error?e.message:'吊销失败')}finally{busy.value=false}}
onMounted(load)
</script>
