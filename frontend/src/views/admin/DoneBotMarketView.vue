<template>
  <AppLayout>
    <div class="min-w-0 space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 class="text-2xl font-bold">Bot 广场管理</h1>
          <p class="mt-1 text-sm text-gray-500">管理用户分享的 Bot 审核、分类、四品类消费返现比例、AI 审核 Agent 与创作者提现打款。</p>
        </div>
        <button class="btn btn-secondary gap-1.5" :disabled="busy" @click="reloadAll">
          <Icon name="refresh" size="sm" />刷新
        </button>
      </div>

      <!-- Overview KPI Cards -->
      <div v-if="overview" class="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <div class="text-xs text-gray-500">已上架 / 总 Bot</div>
          <div class="mt-1 text-xl font-bold">{{ overview.published_listings }} / {{ overview.total_listings }}</div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <div class="text-xs text-gray-500">待审 Bot / 待处理举报</div>
          <div class="mt-1 text-xl font-bold text-amber-600">{{ overview.pending_review_count }} / {{ overview.open_report_count }}</div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <div class="text-xs text-gray-500">累计安装 / 归因消费</div>
          <div class="mt-1 text-xl font-bold">{{ overview.total_installs }} · ¥{{ fmt(overview.total_attributed_cost) }}</div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <div class="text-xs text-gray-500">累计创作者奖励</div>
          <div class="mt-1 text-xl font-bold text-emerald-600">¥{{ fmt(overview.total_creator_reward) }}</div>
        </div>
        <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <div class="text-xs text-gray-500">待审提现 / 累计已打款</div>
          <div class="mt-1 text-xl font-bold">{{ overview.pending_withdrawals }} 笔 · ¥{{ fmt(overview.total_withdrawn) }}</div>
        </div>
      </div>

      <!-- Section Tabs -->
      <div class="flex flex-wrap gap-2 border-b border-gray-200 pb-2 dark:border-dark-700">
        <button
          v-for="t in tabs"
          :key="t.id"
          class="rounded-lg px-3.5 py-2 text-sm font-medium transition"
          :class="tab === t.id ? 'bg-primary-600 text-white' : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700'"
          @click="tab = t.id"
        >
          {{ t.label }}
        </button>
      </div>

      <p v-if="error" role="alert" class="rounded-lg bg-red-50 px-3.5 py-2.5 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>

      <!-- Tab 1: Bot Listings & AI Review -->
      <section v-if="tab === 'listings'" class="space-y-4">
        <div class="flex flex-wrap items-center gap-3">
          <input v-model="listingQuery" class="input min-w-0 flex-1 sm:max-w-sm" placeholder="搜索 Bot 名称、简介或作者邮箱" @keyup.enter="loadListings" />
          <select v-model="listingStatus" class="input w-auto" @change="loadListings">
            <option value="all">全部状态</option>
            <option value="review_queue">待审核队列</option>
            <option value="published">已上架</option>
            <option value="rejected">已驳回</option>
            <option value="suspended">已封禁</option>
            <option value="unlisted">作者下架</option>
          </select>
          <button class="btn btn-secondary" :disabled="busy" @click="loadListings">查询</button>
        </div>

        <p v-if="listingsLoading" class="py-10 text-center text-sm text-gray-500">加载中…</p>
        <p v-else-if="!listings.length" class="py-12 text-center text-sm text-gray-500">暂无符合条件的 Bot</p>
        <div v-else class="divide-y divide-gray-200 rounded-xl border border-gray-200 bg-white dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-800">
          <article v-for="item in listings" :key="item.id" class="flex flex-wrap items-center justify-between gap-4 p-4">
            <div class="min-w-0 flex-1 basis-72">
              <div class="flex flex-wrap items-center gap-2">
                <h3 class="font-semibold">{{ item.name }}</h3>
                <span v-if="item.listing_type === 'studio'" class="rounded bg-indigo-100 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300">AI 工作室 · {{ item.bot_count }} 位员工</span>
                <span v-if="item.eval_verified" class="rounded bg-emerald-100 px-2 py-0.5 text-xs font-medium text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">已实测认证</span>
                <span class="rounded bg-gray-100 px-2 py-0.5 text-xs dark:bg-dark-700">{{ item.category_name }}</span>
                <span class="rounded px-2 py-0.5 text-xs font-medium" :class="statusBadge(item.status)">{{ statusLabel(item.status) }}</span>
                <span v-if="item.featured" class="rounded bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">官方精选</span>
                <span class="text-xs text-gray-500">v{{ item.latest_version }}</span>
              </div>
              <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">{{ item.tagline }}</p>
              <p class="mt-1.5 text-xs text-gray-500">
                作者：{{ item.author_name }} · Skill {{ item.skill_count }} 个 · 安装 {{ item.install_count }} 次 · 归因消费 ¥{{ fmt(item.total_attributed_cost) }} · 奖励 ¥{{ fmt(item.total_creator_reward) }}
                <span v-if="item.report_count > 0" class="ml-2 text-red-600">举报 {{ item.report_count }}</span>
              </p>
              <p v-if="item.last_review_note" class="mt-1 text-xs text-amber-700 dark:text-amber-300">审核备注：{{ item.last_review_note }}</p>
            </div>
            <div class="flex shrink-0 flex-wrap gap-2">
              <button class="btn btn-secondary text-xs" :disabled="busy" @click="openDetail(item.id)">查看 / 审核</button>
              <button v-if="item.status === 'published'" class="btn btn-secondary text-xs" :disabled="busy" @click="openCategoryDialog(item)">修改分类</button>
              <button class="btn btn-secondary text-xs" :disabled="busy" @click="toggleFeatured(item)">{{ item.featured ? '取消精选' : '设为精选' }}</button>
              <button v-if="item.status === 'suspended'" class="btn btn-secondary text-xs text-red-600" :disabled="busy" @click="openDeleteDialog(item)">永久删除</button>
            </div>
          </article>
        </div>
      </section>

      <!-- Tab 2: Categories -->
      <section v-else-if="tab === 'categories'" class="space-y-4">
        <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
          <h2 class="mb-3 text-sm font-semibold">{{ editingCategory.id ? '编辑分类' : '新增分类' }}</h2>
          <div class="grid gap-3 sm:grid-cols-4">
            <input v-model="editingCategory.name" class="input" placeholder="分类名称（如：设计创作）" />
            <input v-model="editingCategory.slug" class="input" placeholder="英文标识（如：design）" />
            <input v-model="editingCategory.description" class="input" placeholder="分类简介（选填）" />
            <div class="flex items-center gap-2">
              <input v-model.number="editingCategory.sort_order" type="number" class="input w-24" placeholder="排序" />
              <label class="flex items-center gap-1.5 text-xs whitespace-nowrap"><input v-model="editingCategory.enabled" type="checkbox" />启用</label>
            </div>
          </div>
          <div class="mt-3 flex gap-2">
            <button class="btn btn-primary text-sm" :disabled="busy" @click="saveCategory">{{ editingCategory.id ? '保存修改' : '添加分类' }}</button>
            <button v-if="editingCategory.id" class="btn btn-secondary text-sm" :disabled="busy" @click="resetCategoryForm">取消编辑</button>
          </div>
        </div>

        <div class="divide-y divide-gray-200 rounded-xl border border-gray-200 bg-white dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-800">
          <div v-for="cat in categories" :key="cat.id" class="flex flex-wrap items-center justify-between gap-3 p-4">
            <div>
              <div class="flex items-center gap-2">
                <span class="font-semibold">{{ cat.name }}</span>
                <code class="rounded bg-gray-100 px-1.5 py-0.5 text-xs dark:bg-dark-700">{{ cat.slug }}</code>
                <span class="text-xs text-gray-500">排序 {{ cat.sort_order }}</span>
                <span class="rounded px-2 py-0.5 text-xs" :class="cat.enabled ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-500'">{{ cat.enabled ? '已启用' : '已停用' }}</span>
              </div>
              <p class="mt-1 text-xs text-gray-500">{{ cat.description || '无简介' }} · 已上架 Bot {{ cat.listing_count ?? 0 }} 个</p>
            </div>
            <div class="flex gap-2">
              <button class="btn btn-secondary text-xs" :disabled="busy" @click="editCategory(cat)">编辑</button>
              <button class="btn btn-secondary text-xs text-red-600" :disabled="busy" @click="removeCategory(cat)">删除</button>
            </div>
          </div>
        </div>
      </section>

      <!-- Tab 3: Withdrawals -->
      <section v-else-if="tab === 'withdrawals'" class="space-y-4">
        <div class="flex items-center gap-3">
          <select v-model="withdrawStatus" class="input w-auto" @change="loadWithdrawals">
            <option value="all">全部提现单</option>
            <option value="pending">待审核打款</option>
            <option value="paid">已打款</option>
            <option value="rejected">已驳回</option>
          </select>
        </div>

        <p v-if="!withdrawals.length" class="py-12 text-center text-sm text-gray-500">暂无提现申请</p>
        <div v-else class="divide-y divide-gray-200 rounded-xl border border-gray-200 bg-white dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-800">
          <div v-for="w in withdrawals" :key="w.id" class="flex flex-wrap items-center justify-between gap-4 p-4">
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-base font-bold text-emerald-600">¥{{ fmt(w.amount) }}</span>
                <span class="rounded px-2 py-0.5 text-xs font-medium" :class="withdrawBadge(w.status)">{{ withdrawLabel(w.status) }}</span>
                <span class="text-sm font-medium">{{ w.creator_name || w.creator_email }}</span>
                <span class="text-xs text-gray-500">#{{ w.id }} · {{ fmtTime(w.created_at) }}</span>
              </div>
              <p class="mt-1 text-sm">支付宝账号：<strong>{{ w.alipay_account }}</strong> · 收款人实名：<strong>{{ w.alipay_real_name }}</strong></p>
              <p v-if="w.payout_trade_no" class="mt-1 text-xs text-emerald-700 dark:text-emerald-300">打款流水号：{{ w.payout_trade_no }}</p>
              <p v-if="w.admin_note" class="mt-1 text-xs text-gray-500">备注：{{ w.admin_note }}</p>
            </div>
            <div v-if="w.status === 'pending'" class="flex shrink-0 gap-2">
              <button class="btn btn-primary text-xs" :disabled="busy" @click="openWithdrawDialog(w, 'paid')">标记已打款</button>
              <button class="btn btn-secondary text-xs text-red-600" :disabled="busy" @click="openWithdrawDialog(w, 'rejected')">驳回退回</button>
            </div>
          </div>
        </div>
      </section>

      <!-- Tab 4: Reports -->
      <section v-else-if="tab === 'reports'" class="space-y-4">
        <p v-if="!reports.length" class="py-12 text-center text-sm text-gray-500">暂无举报记录</p>
        <div v-else class="divide-y divide-gray-200 rounded-xl border border-gray-200 bg-white dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-800">
          <div v-for="r in reports" :key="r.id" class="flex flex-wrap items-center justify-between gap-4 p-4">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="font-semibold">{{ r.listing_name }}</span>
                <span class="rounded bg-red-100 px-2 py-0.5 text-xs text-red-700 dark:bg-red-900/30 dark:text-red-300">{{ r.reason_type }}</span>
                <span class="text-xs text-gray-500">{{ r.status }} · {{ fmtTime(r.created_at) }}</span>
              </div>
              <p class="mt-1 text-sm text-gray-600 dark:text-gray-300">{{ r.detail || '无补充说明' }}</p>
              <p class="mt-1 text-xs text-gray-500">举报人：{{ r.reporter_email }}</p>
            </div>
            <div class="flex shrink-0 gap-2">
              <button class="btn btn-secondary text-xs" :disabled="busy" @click="openDetail(r.listing_id)">查看 Bot</button>
              <button v-if="r.status === 'open'" class="btn btn-primary text-xs" :disabled="busy" @click="resolveReport(r.id, 'resolved')">标记已处理</button>
              <button v-if="r.status === 'open'" class="btn btn-secondary text-xs" :disabled="busy" @click="resolveReport(r.id, 'dismissed')">忽略</button>
            </div>
          </div>
        </div>
      </section>

      <!-- Tab 5: Settings (4-category rebate rates + AI Review Agent + withdrawal rules) -->
      <section v-else-if="tab === 'settings' && settingsDraft" class="space-y-5">
        <div class="rounded-xl border border-gray-200 bg-white p-5 space-y-5 dark:border-dark-700 dark:bg-dark-800">
          <div class="flex flex-wrap items-center gap-6">
            <label class="flex items-center gap-2 text-sm font-medium"><input v-model="settingsDraft.enabled" type="checkbox" />启用 Bot 分享广场</label>
            <label class="flex items-center gap-2 text-sm font-medium"><input v-model="settingsDraft.auto_publish_on_ai_approve" type="checkbox" />AI 安全审核通过后自动上架（取消则转人工确认）</label>
          </div>

          <div class="border-t border-gray-200 pt-4 dark:border-dark-700">
            <h3 class="mb-3 text-sm font-semibold">四品类消费返现比例（%，设为 0 即关闭该品类分润）</h3>
            <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">LLM Token 返现 (%)</span><input v-model.number="settingsDraft.rebate_llm_percent" type="number" step="0.1" min="0" max="50" class="input" /></label>
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">图片生成返现 (%)</span><input v-model.number="settingsDraft.rebate_image_percent" type="number" step="0.1" min="0" max="50" class="input" /></label>
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">视频生成返现 (%)</span><input v-model.number="settingsDraft.rebate_video_percent" type="number" step="0.1" min="0" max="50" class="input" /></label>
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">音乐生成返现 (%)</span><input v-model.number="settingsDraft.rebate_music_percent" type="number" step="0.1" min="0" max="50" class="input" /></label>
            </div>
          </div>

          <div class="border-t border-gray-200 pt-4 dark:border-dark-700">
            <h3 class="mb-3 text-sm font-semibold">归因周期、冻结与提现/转算力参数</h3>
            <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">安装归因有效期（天）</span><input v-model.number="settingsDraft.attribution_days" type="number" min="1" max="3650" class="input" /></label>
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">分润冻结天数（天）</span><input v-model.number="settingsDraft.freeze_days" type="number" min="0" max="180" class="input" /></label>
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">最低提现金额（CNY 元）</span><input v-model.number="settingsDraft.min_withdraw_amount" type="number" step="1" min="1" class="input" /></label>
              <label class="block text-sm"><span class="mb-1 block text-gray-600 dark:text-gray-300">转算力余额额外赠送 (%)</span><input v-model.number="settingsDraft.transfer_bonus_percent" type="number" step="0.5" min="0" max="100" class="input" /></label>
            </div>
          </div>

          <div class="border-t border-gray-200 pt-4 dark:border-dark-700 space-y-3">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div>
                <h3 class="text-sm font-semibold">专业安全审核 Agent 配置</h3>
                <p class="mt-0.5 text-xs text-gray-500">直接从网关已配置的上游账号与模型中选择即可，支持 OpenAI 兼容、Anthropic、Gemini 及账号已绑定的代理。</p>
              </div>
              <button class="btn btn-secondary text-xs" :disabled="busy || testingReviewer" @click="runReviewerTest">
                {{ testingReviewer ? '正在测试连通性…' : '测试审核模型连通性' }}
              </button>
            </div>

            <div class="grid gap-4 sm:grid-cols-2">
              <label class="block text-sm">
                <span class="mb-1 block text-gray-600 dark:text-gray-300">选择审核上游账号</span>
                <select v-model.number="selectedReviewAccountId" class="input" @change="onReviewAccountChange">
                  <option :value="0">自动匹配可用活跃账号</option>
                  <option v-for="acc in reviewAccounts" :key="acc.id" :value="acc.id">
                    {{ acc.name }}（{{ acc.platform }} · {{ acc.type }}{{ acc.status !== 'active' ? ' · 未激活' : '' }}）
                  </option>
                </select>
              </label>

              <label class="block text-sm">
                <span class="mb-1 block text-gray-600 dark:text-gray-300">
                  选择或输入审核模型
                  <span v-if="loadingAccountModels" class="ml-1 text-xs text-gray-400">（正在拉取该账号可用模型…）</span>
                </span>
                <div class="flex gap-2">
                  <select
                    v-if="availableReviewModels.length"
                    class="input w-1/2"
                    :value="availableReviewModels.includes(settingsDraft.review_model) ? settingsDraft.review_model : ''"
                    @change="(e) => { const v = (e.target as HTMLSelectElement).value; if (v) settingsDraft!.review_model = v }"
                  >
                    <option value="">从列表快速选择…</option>
                    <option v-for="m in availableReviewModels" :key="m" :value="m">{{ m }}</option>
                  </select>
                  <input
                    v-model="settingsDraft.review_model"
                    class="input flex-1"
                    placeholder="如 deepseek-chat / gpt-4o-mini / claude-haiku-4-5"
                  />
                </div>
              </label>
            </div>

            <div
              v-if="reviewTestResult"
              class="rounded-xl px-3.5 py-2.5 text-xs"
              :class="reviewTestResult.ok ? 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-200' : 'bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300'"
            >
              <template v-if="reviewTestResult.ok">
                ✓ 测试通过 · 通道：{{ reviewTestResult.account }} · 模型：{{ reviewTestResult.model }} · 耗时 {{ reviewTestResult.latency_ms }}ms · 样本判定：{{ reviewTestResult.verdict }}（风险分 {{ reviewTestResult.risk_score }}）
              </template>
              <template v-else>
                ✗ 测试失败{{ reviewTestResult.account ? `（${reviewTestResult.account} / ${reviewTestResult.model}）` : '' }}：{{ reviewTestResult.error }}
              </template>
            </div>

            <details v-if="selectedReviewAccountId === 0" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
              <summary class="cursor-pointer text-xs font-medium text-gray-600 dark:text-gray-300">
                高级：自定义独立审核 API（仅当上方选择「自动匹配」时可选填）
              </summary>
              <div class="mt-3 grid gap-4 sm:grid-cols-2">
                <label class="block text-sm">
                  <span class="mb-1 block text-gray-600 dark:text-gray-300">独立审核 API Base</span>
                  <input
                    v-model="settingsDraft.review_api_base"
                    type="url"
                    name="market-review-custom-base"
                    autocomplete="off"
                    class="input"
                    placeholder="https://api.deepseek.com/v1（留空则自动匹配系统账号）"
                  />
                </label>
                <label class="block text-sm">
                  <span class="mb-1 block text-gray-600 dark:text-gray-300">独立审核 API Key（{{ settingsDraft.review_api_key_configured ? '已配置，留空保持不变' : '选填' }}）</span>
                  <div class="flex gap-2">
                    <input
                      v-model="reviewKeyInput"
                      type="password"
                      name="market-review-custom-key"
                      autocomplete="new-password"
                      class="input flex-1"
                      placeholder="留空不修改"
                    />
                    <button
                      v-if="settingsDraft.review_api_key_configured"
                      type="button"
                      class="btn btn-secondary shrink-0 text-xs"
                      @click="clearCustomReviewAPI"
                    >
                      清空独立密钥
                    </button>
                  </div>
                </label>
              </div>
            </details>
          </div>

          <div class="flex justify-end">
            <button class="btn btn-primary" :disabled="busy" @click="saveSettings">{{ busy ? '保存中…' : '保存配置' }}</button>
          </div>
        </div>
      </section>
    </div>

    <!-- Listing Review Modal -->
    <BaseDialog :show="detailOpen" :title="activeDetail ? `${listingKindLabel(activeDetail)}审核：${activeDetail.name}` : '详情'" width="wide" @close="detailOpen = false">
      <div v-if="activeDetail" class="space-y-4">
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <span class="rounded px-2 py-0.5 text-xs font-medium" :class="statusBadge(activeDetail.status)">{{ statusLabel(activeDetail.status) }}</span>
          <span v-if="activeDetail.listing_type === 'studio'" class="rounded bg-indigo-100 px-2 py-0.5 text-xs font-medium text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300">部门 / AI 工作室 · {{ activeDetail.bot_count }} 位员工</span>
          <template v-if="activeDetail.status === 'published'">
            <span class="text-gray-500">分类：</span>
            <select v-model.number="detailCategoryId" class="input w-auto py-1 text-xs" :disabled="busy">
              <option v-for="cat in enabledCategories" :key="cat.id" :value="cat.id">{{ cat.name }}</option>
            </select>
            <button
              class="btn btn-secondary py-1 text-xs"
              :disabled="busy || detailCategoryId <= 0 || detailCategoryId === activeDetail.category_id"
              @click="saveDetailCategory"
            >
              保存分类
            </button>
          </template>
          <span v-else>分类：{{ activeDetail.category_name }}</span>
          <span>作者：{{ activeDetail.author_name }}</span>
          <span>版本：v{{ activeDetail.latest_version }}</span>
        </div>
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ activeDetail.tagline }} — {{ activeDetail.description }}</p>

        <!-- AI Review Agent Report -->
        <div v-if="activeDetail.latest_release?.ai_report" class="rounded-xl border border-gray-200 bg-gray-50 p-4 text-sm dark:border-dark-700 dark:bg-dark-900">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <strong>AI 专业审核 Agent 报告</strong>
            <span class="text-xs">判定：<strong>{{ activeDetail.latest_release.ai_report.verdict }}</strong> · 风险分：<strong>{{ activeDetail.latest_release.ai_report.risk_score }}/100</strong> · 模型：{{ activeDetail.latest_release.ai_report.model || '—' }}</span>
          </div>
          <p class="mt-1.5 text-xs text-gray-600 dark:text-gray-300">{{ activeDetail.latest_release.ai_report.summary }}</p>
          <div v-if="activeDetail.latest_release.ai_report.dimensions" class="mt-2 grid gap-2 sm:grid-cols-2">
            <div v-for="(dim, key) in activeDetail.latest_release.ai_report.dimensions" :key="key" class="rounded bg-white p-2 text-xs dark:bg-dark-800">
              <span :class="dim.passed ? 'text-emerald-600' : 'text-red-600'">{{ dim.passed ? '✓' : '✗' }} {{ reviewDimensionLabel(String(key)) }} ({{ dim.score }})</span>
              <p class="mt-0.5 text-gray-500">{{ dim.finding }}</p>
            </div>
          </div>
          <ul v-if="activeDetail.latest_release.ai_report.rejection_reasons?.length" class="mt-2 list-disc pl-5 text-xs text-red-600">
            <li v-for="(r, i) in activeDetail.latest_release.ai_report.rejection_reasons" :key="i">{{ r }}</li>
          </ul>
        </div>

        <details open class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
          <summary class="cursor-pointer text-sm font-medium">SOUL.md 完整内容</summary>
          <pre class="mt-2 max-h-60 overflow-auto whitespace-pre-wrap break-words text-xs">{{ activeDetail.latest_release?.soul_text || activeDetail.soul_preview }}</pre>
        </details>

        <details v-if="activeDetail.skills?.length" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700">
          <summary class="cursor-pointer text-sm font-medium">内置 Skill（{{ activeDetail.skills.length }} 个）</summary>
          <div class="mt-2 space-y-3">
            <div v-for="sk in activeDetail.skills" :key="sk.name" class="rounded bg-gray-50 p-2.5 dark:bg-dark-900">
              <div class="text-xs font-semibold">
                {{ sk.name }}
                <span class="font-normal text-gray-500">{{ sk.description }}</span>
                <span v-if="sk.files?.length" class="ml-1.5 rounded bg-blue-100 px-1.5 py-0.5 text-[11px] font-normal text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">
                  +{{ sk.files.length }} 个附属文件
                </span>
              </div>
              <pre class="mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-words text-xs">{{ sk.instructions }}</pre>
              <div v-if="sk.files?.length" class="mt-2 space-y-1.5 border-t border-gray-200 pt-2 dark:border-dark-700">
                <details
                  v-for="file in sk.files"
                  :key="file.path"
                  class="rounded border border-gray-200 bg-white px-2 py-1.5 dark:border-dark-700 dark:bg-dark-800"
                >
                  <summary class="cursor-pointer font-mono text-[11px] font-medium text-gray-700 dark:text-gray-200">
                    {{ file.path }}
                  </summary>
                  <pre class="mt-1.5 max-h-44 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] text-gray-600 dark:text-gray-300">{{ file.content }}</pre>
                </details>
              </div>
            </div>
          </div>
        </details>

        <label class="block text-sm">
          <span class="mb-1 block font-medium">审核备注 / 驳回原因</span>
          <textarea v-model="reviewNote" class="input min-h-20" placeholder="填写审核意见、驳回原因或封禁理由…" />
        </label>
      </div>
      <template #footer>
        <button class="btn btn-secondary" :disabled="busy" @click="detailOpen = false">关闭</button>
        <button class="btn btn-secondary" :disabled="busy" @click="submitListingReview('rerun_ai')">重跑 AI 审核</button>
        <button class="btn btn-secondary text-red-600" :disabled="busy" @click="submitListingReview('reject')">驳回</button>
        <button v-if="activeDetail?.status !== 'suspended'" class="btn btn-secondary text-red-700" :disabled="busy" @click="submitListingReview('suspend')">封禁下架</button>
        <button v-if="activeDetail?.status === 'suspended'" class="btn btn-secondary text-red-700" :disabled="busy" @click="openDeleteDialog(activeDetail)">永久删除</button>
        <button class="btn btn-primary" :disabled="busy" @click="submitListingReview('approve')">审核通过并上架</button>
      </template>
    </BaseDialog>

    <!-- Change Category Modal -->
    <BaseDialog :show="categoryDialogOpen" :title="categoryTarget ? `修改${listingKindLabel(categoryTarget)}分类：${categoryTarget.name}` : '修改分类'" @close="categoryDialogOpen = false">
      <div v-if="categoryTarget" class="space-y-4 text-sm">
        <p class="text-gray-600 dark:text-gray-300">
          当前分类：<strong>{{ categoryTarget.category_name }}</strong>
        </p>
        <label class="block">
          <span class="mb-1 block font-medium">目标分类</span>
          <select v-model.number="targetCategoryId" class="input" :disabled="busy">
            <option v-for="cat in enabledCategories" :key="cat.id" :value="cat.id">{{ cat.name }}（{{ cat.slug }}）</option>
          </select>
        </label>
        <p class="text-xs text-gray-500">仅更新广场展示分类，不影响已上架版本、安装记录与创作者分润统计。</p>
      </div>
      <template #footer>
        <button class="btn btn-secondary" :disabled="busy" @click="categoryDialogOpen = false">取消</button>
        <button
          class="btn btn-primary"
          :disabled="busy || !categoryTarget || targetCategoryId <= 0 || targetCategoryId === categoryTarget.category_id"
          @click="submitCategoryChange"
        >
          保存分类
        </button>
      </template>
    </BaseDialog>

    <!-- Delete Suspended Listing Confirmation Modal -->
    <BaseDialog :show="deleteDialogOpen" :title="deleteTarget ? `永久删除已封禁${listingKindLabel(deleteTarget)}` : '永久删除'" @close="deleteDialogOpen = false">
      <div v-if="deleteTarget" class="space-y-3 text-sm">
        <p>
          确认永久删除已封禁{{ listingKindLabel(deleteTarget) }} <strong>{{ deleteTarget.name }}</strong>（作者：{{ deleteTarget.author_name }} · v{{ deleteTarget.latest_version }}）？
        </p>
        <p class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-900/20 dark:text-red-300">
          此操作不可撤销：将同时清除该条目的历史发布版本、已吊销的安装归因记录与相关举报；已结算的创作者财务分润账本仍会保留以供对账。
        </p>
      </div>
      <template #footer>
        <button class="btn btn-secondary" :disabled="busy" @click="deleteDialogOpen = false">取消</button>
        <button class="btn btn-danger" :disabled="busy" @click="confirmDeleteListing">确认永久删除</button>
      </template>
    </BaseDialog>

    <!-- Withdrawal Process Modal -->
    <BaseDialog :show="withdrawDialogOpen" :title="withdrawDecision === 'paid' ? '确认支付宝打款完成' : '驳回提现申请'" @close="withdrawDialogOpen = false">
      <div v-if="activeWithdraw" class="space-y-4 text-sm">
        <p>创作者：<strong>{{ activeWithdraw.creator_name || activeWithdraw.creator_email }}</strong> · 金额：<strong>¥{{ fmt(activeWithdraw.amount) }}</strong></p>
        <p>支付宝：<strong>{{ activeWithdraw.alipay_account }}</strong>（{{ activeWithdraw.alipay_real_name }}）</p>
        <label v-if="withdrawDecision === 'paid'" class="block">
          <span class="mb-1 block font-medium">支付宝转账流水号</span>
          <input v-model="withdrawTradeNo" class="input" placeholder="填写支付宝打款单号以便对账" />
        </label>
        <label class="block">
          <span class="mb-1 block font-medium">{{ withdrawDecision === 'paid' ? '打款备注（选填）' : '驳回原因（必填）' }}</span>
          <textarea v-model="withdrawNote" class="input min-h-20" />
        </label>
      </div>
      <template #footer>
        <button class="btn btn-secondary" :disabled="busy" @click="withdrawDialogOpen = false">取消</button>
        <button class="btn btn-primary" :disabled="busy" @click="submitWithdrawProcess">确认提交</button>
      </template>
    </BaseDialog>

    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { accountsAPI } from '@/api/admin/accounts'
import botMarketAdmin, {
  type MarketCategory,
  type MarketListing,
  type MarketListingDetail,
  type MarketOverview,
  type MarketReport,
  type MarketSettings,
  type MarketWithdrawal,
  type ReviewAccountOption,
  type ReviewTestResult,
} from '@/api/admin/donebot-market'
import { useAppStore } from '@/stores/app'

const app = useAppStore()
const stepUp = useStepUp()

const tabs = [
  { id: 'listings', label: 'Bot 审核与上架' },
  { id: 'categories', label: '分类管理' },
  { id: 'withdrawals', label: '提现审核' },
  { id: 'reports', label: '用户举报' },
  { id: 'settings', label: '分润与 AI 审核配置' },
] as const

const REVIEW_DIMENSION_LABELS: Record<string, string> = {
  token_farming: '刷量与算力消费',
  malicious_loop: '恶意循环与跨 Bot 乒乓',
  sensitive_data_exfil: '敏感文件与外泄风险',
  prompt_injection: '越权与指令注入',
  routine_and_automation_safety: '例行任务与自动化安全',
  org_architecture_coherence: '组织分工与协作合理性',
  content_compliance: '内容合规与实用价值',
}
function reviewDimensionLabel(key: string): string {
  return REVIEW_DIMENSION_LABELS[key] || key
}

const tab = ref<(typeof tabs)[number]['id']>('listings')
const busy = ref(false)
const error = ref('')
const overview = ref<MarketOverview | null>(null)

const listings = ref<MarketListing[]>([])
const listingsLoading = ref(false)
const listingStatus = ref('all')
const listingQuery = ref('')

const categories = ref<MarketCategory[]>([])
const editingCategory = ref<{ id?: number; slug: string; name: string; description: string; sort_order: number; enabled: boolean }>({
  slug: '', name: '', description: '', sort_order: 50, enabled: true,
})

const withdrawals = ref<MarketWithdrawal[]>([])
const withdrawStatus = ref('all')
const reports = ref<MarketReport[]>([])

const settingsDraft = ref<MarketSettings | null>(null)
const selectedReviewAccountId = ref<number>(0)
const reviewAccounts = ref<ReviewAccountOption[]>([])
const reviewAccountLiveModels = ref<string[]>([])
const loadingAccountModels = ref(false)
const reviewGlobalModels = ref<string[]>([])
const reviewKeyInput = ref('')
const clearReviewKeyFlag = ref(false)
const testingReviewer = ref(false)
const reviewTestResult = ref<ReviewTestResult | null>(null)

const availableReviewModels = computed(() => {
  if (selectedReviewAccountId.value > 0) {
    if (reviewAccountLiveModels.value.length > 0) return reviewAccountLiveModels.value
    const chosen = reviewAccounts.value.find(a => a.id === selectedReviewAccountId.value)
    return chosen?.models || []
  }
  return reviewGlobalModels.value
})

async function loadModelsForAccount(accountId: number, autoSelectDefault: boolean) {
  reviewAccountLiveModels.value = []
  if (accountId <= 0) return
  loadingAccountModels.value = true
  try {
    const list = await accountsAPI.getAvailableModels(accountId)
    const ids = Array.from(new Set((list || []).map(m => String(m.id || '').trim()).filter(Boolean)))
    reviewAccountLiveModels.value = ids
    if (autoSelectDefault && settingsDraft.value && ids.length > 0 && !ids.includes(settingsDraft.value.review_model)) {
      settingsDraft.value.review_model = ids[0]
    }
  } catch {
    const chosen = reviewAccounts.value.find(a => a.id === accountId)
    if (autoSelectDefault && chosen?.default_model && settingsDraft.value && !(chosen.models || []).includes(settingsDraft.value.review_model)) {
      settingsDraft.value.review_model = chosen.default_model
    }
  } finally {
    loadingAccountModels.value = false
  }
}

function onReviewAccountChange() {
  reviewTestResult.value = null
  void loadModelsForAccount(selectedReviewAccountId.value, true)
}

function clearCustomReviewAPI() {
  if (!settingsDraft.value) return
  settingsDraft.value.review_api_base = ''
  reviewKeyInput.value = ''
  clearReviewKeyFlag.value = true
  settingsDraft.value.review_api_key_configured = false
}

const detailOpen = ref(false)
const activeDetail = ref<MarketListingDetail | null>(null)
const detailCategoryId = ref<number>(0)
const reviewNote = ref('')

const categoryDialogOpen = ref(false)
const categoryTarget = ref<MarketListing | null>(null)
const targetCategoryId = ref<number>(0)

const deleteDialogOpen = ref(false)
const deleteTarget = ref<MarketListing | null>(null)

const enabledCategories = computed(() => categories.value.filter(c => c.enabled))

function listingKindLabel(item: Pick<MarketListing, 'listing_type'>) {
  return item.listing_type === 'studio' ? '部门' : 'Bot'
}

const withdrawDialogOpen = ref(false)
const activeWithdraw = ref<MarketWithdrawal | null>(null)
const withdrawDecision = ref<'paid' | 'rejected'>('paid')
const withdrawTradeNo = ref('')
const withdrawNote = ref('')

function errMsg(cause: unknown) {
  const value = cause as { response?: { data?: { message?: string } }; message?: string }
  return value?.response?.data?.message || value?.message || '操作失败，请重试'
}
function fmt(n: number) { return Number(n || 0).toFixed(2) }
function fmtTime(iso: string) { return iso ? new Date(iso).toLocaleString() : '' }
function statusLabel(s: string) {
  const map: Record<string, string> = {
    pending_review: 'AI 审核中', manual_review: '待人工复核', published: '已上架',
    rejected: '已驳回', unlisted: '作者下架', suspended: '已封禁',
  }
  return map[s] || s
}
function statusBadge(s: string) {
  if (s === 'published') return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
  if (s === 'manual_review' || s === 'pending_review') return 'bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300'
  if (s === 'rejected' || s === 'suspended') return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
}
function withdrawLabel(s: string) { return s === 'paid' ? '已打款' : s === 'rejected' ? '已驳回' : '待打款' }
function withdrawBadge(s: string) {
  if (s === 'paid') return 'bg-emerald-100 text-emerald-700'
  if (s === 'rejected') return 'bg-red-100 text-red-700'
  return 'bg-amber-100 text-amber-800'
}

async function loadListings() {
  listingsLoading.value = true
  try {
    const res = await botMarketAdmin.listings({ status: listingStatus.value, q: listingQuery.value, page: 1, page_size: 50 })
    listings.value = res.items || []
  } catch (c) { error.value = errMsg(c) } finally { listingsLoading.value = false }
}
async function loadWithdrawals() {
  try {
    const res = await botMarketAdmin.withdrawals({ status: withdrawStatus.value, page: 1, page_size: 50 })
    withdrawals.value = res.items || []
  } catch (c) { error.value = errMsg(c) }
}
async function reloadAll() {
  busy.value = true; error.value = ''
  try {
    const [ov, cats, cfg, reps, opts] = await Promise.all([
      botMarketAdmin.overview(),
      botMarketAdmin.categories(),
      botMarketAdmin.settings(),
      botMarketAdmin.reports({ status: 'all', page: 1, page_size: 50 }),
      botMarketAdmin.reviewOptions().catch(() => ({ accounts: [], models: [] })),
    ])
    overview.value = ov
    categories.value = cats || []
    settingsDraft.value = cfg
    selectedReviewAccountId.value = cfg.review_account_id || 0
    clearReviewKeyFlag.value = false
    reviewAccounts.value = opts.accounts || []
    reviewGlobalModels.value = opts.models || []
    if (selectedReviewAccountId.value > 0) {
      void loadModelsForAccount(selectedReviewAccountId.value, false)
    }
    reports.value = reps.items || []
    await Promise.all([loadListings(), loadWithdrawals()])
  } catch (c) { error.value = errMsg(c) } finally { busy.value = false }
}

async function openDetail(id: string) {
  busy.value = true; error.value = ''
  try {
    activeDetail.value = await botMarketAdmin.listingDetail(id)
    detailCategoryId.value = activeDetail.value.category_id
    reviewNote.value = activeDetail.value.last_review_note || ''
    detailOpen.value = true
  } catch (c) { error.value = errMsg(c) } finally { busy.value = false }
}

function openCategoryDialog(item: MarketListing) {
  categoryTarget.value = item
  targetCategoryId.value = item.category_id
  categoryDialogOpen.value = true
}

async function submitCategoryChange() {
  if (!categoryTarget.value || targetCategoryId.value <= 0) return
  busy.value = true; error.value = ''
  try {
    const updated = await stepUp.run(() => botMarketAdmin.updateListingCategory(categoryTarget.value!.id, targetCategoryId.value))
    if (activeDetail.value && activeDetail.value.id === updated.id) {
      activeDetail.value = updated
      detailCategoryId.value = updated.category_id
    }
    app.showSuccess(`${listingKindLabel(updated)}分类已更新为「${updated.category_name}」`)
    categoryDialogOpen.value = false
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

async function saveDetailCategory() {
  if (!activeDetail.value || detailCategoryId.value <= 0) return
  busy.value = true; error.value = ''
  try {
    const updated = await stepUp.run(() => botMarketAdmin.updateListingCategory(activeDetail.value!.id, detailCategoryId.value))
    activeDetail.value = updated
    detailCategoryId.value = updated.category_id
    app.showSuccess(`${listingKindLabel(updated)}分类已更新为「${updated.category_name}」`)
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

function openDeleteDialog(item: MarketListing) {
  deleteTarget.value = item
  deleteDialogOpen.value = true
}

async function confirmDeleteListing() {
  if (!deleteTarget.value) return
  const target = deleteTarget.value
  busy.value = true; error.value = ''
  try {
    await stepUp.run(() => botMarketAdmin.deleteListing(target.id))
    app.showSuccess(`已永久删除封禁${listingKindLabel(target)}「${target.name}」`)
    deleteDialogOpen.value = false
    deleteTarget.value = null
    if (activeDetail.value && activeDetail.value.id === target.id) {
      detailOpen.value = false
      activeDetail.value = null
    }
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

async function submitListingReview(decision: string) {
  if (!activeDetail.value) return
  busy.value = true; error.value = ''
  try {
    activeDetail.value = await stepUp.run(() => botMarketAdmin.reviewListing(activeDetail.value!.id, { decision, note: reviewNote.value }))
    app.showSuccess('审核状态已更新')
    detailOpen.value = false
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

async function toggleFeatured(item: MarketListing) {
  busy.value = true; error.value = ''
  try {
    await stepUp.run(() => botMarketAdmin.reviewListing(item.id, { decision: 'feature', featured: !item.featured }))
    await loadListings()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

function editCategory(cat: MarketCategory) {
  editingCategory.value = { id: cat.id, slug: cat.slug, name: cat.name, description: cat.description, sort_order: cat.sort_order, enabled: cat.enabled }
}
function resetCategoryForm() {
  editingCategory.value = { slug: '', name: '', description: '', sort_order: 50, enabled: true }
}
async function saveCategory() {
  busy.value = true; error.value = ''
  try {
    await stepUp.run(() => botMarketAdmin.saveCategory(editingCategory.value))
    app.showSuccess('分类已保存')
    resetCategoryForm()
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}
async function removeCategory(cat: MarketCategory) {
  busy.value = true; error.value = ''
  try {
    await stepUp.run(() => botMarketAdmin.deleteCategory(cat.id))
    app.showSuccess('分类已删除')
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

function openWithdrawDialog(w: MarketWithdrawal, decision: 'paid' | 'rejected') {
  activeWithdraw.value = w
  withdrawDecision.value = decision
  withdrawTradeNo.value = ''
  withdrawNote.value = ''
  withdrawDialogOpen.value = true
}
async function submitWithdrawProcess() {
  if (!activeWithdraw.value) return
  busy.value = true; error.value = ''
  try {
    await stepUp.run(() => botMarketAdmin.processWithdrawal(activeWithdraw.value!.id, {
      decision: withdrawDecision.value,
      payout_trade_no: withdrawTradeNo.value,
      admin_note: withdrawNote.value,
    }))
    app.showSuccess(withdrawDecision.value === 'paid' ? '已标记打款完成' : '已驳回并退回创作者可用余额')
    withdrawDialogOpen.value = false
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

async function resolveReport(id: number, status: 'resolved' | 'dismissed') {
  busy.value = true; error.value = ''
  try {
    await stepUp.run(() => botMarketAdmin.handleReport(id, { status }))
    app.showSuccess('举报已处理')
    await reloadAll()
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

async function runReviewerTest() {
  if (!settingsDraft.value) return
  testingReviewer.value = true; error.value = ''; reviewTestResult.value = null
  try {
    const useSystemAccount = selectedReviewAccountId.value > 0
    reviewTestResult.value = await stepUp.run(() => botMarketAdmin.testReviewer({
      review_account_id: selectedReviewAccountId.value,
      review_model: settingsDraft.value!.review_model,
      ...(!useSystemAccount && settingsDraft.value!.review_api_base ? { review_api_base: settingsDraft.value!.review_api_base } : {}),
      ...(!useSystemAccount && reviewKeyInput.value.trim() ? { review_api_key: reviewKeyInput.value.trim() } : {}),
    }))
  } catch (c) {
    if (!isStepUpCancelled(c)) error.value = errMsg(c)
  } finally {
    testingReviewer.value = false
  }
}

async function saveSettings() {
  if (!settingsDraft.value) return
  busy.value = true; error.value = ''
  try {
    const useSystemAccount = selectedReviewAccountId.value > 0
    const saved = await stepUp.run(() => botMarketAdmin.saveSettings({
      enabled: settingsDraft.value!.enabled,
      auto_publish_on_ai_approve: settingsDraft.value!.auto_publish_on_ai_approve,
      review_account_id: selectedReviewAccountId.value,
      review_model: settingsDraft.value!.review_model,
      review_api_base: useSystemAccount ? '' : settingsDraft.value!.review_api_base,
      ...(!useSystemAccount && reviewKeyInput.value.trim() ? { review_api_key: reviewKeyInput.value.trim() } : {}),
      ...(useSystemAccount || clearReviewKeyFlag.value ? { clear_review_api_key: true } : {}),
      attribution_days: settingsDraft.value!.attribution_days,
      freeze_days: settingsDraft.value!.freeze_days,
      min_withdraw_amount: settingsDraft.value!.min_withdraw_amount,
      transfer_bonus_percent: settingsDraft.value!.transfer_bonus_percent,
      rebate_llm_percent: settingsDraft.value!.rebate_llm_percent,
      rebate_image_percent: settingsDraft.value!.rebate_image_percent,
      rebate_video_percent: settingsDraft.value!.rebate_video_percent,
      rebate_music_percent: settingsDraft.value!.rebate_music_percent,
    }))
    settingsDraft.value = saved
    selectedReviewAccountId.value = saved.review_account_id || 0
    reviewKeyInput.value = ''
    clearReviewKeyFlag.value = false
    app.showSuccess('Bot 广场与分润配置已保存')
  } catch (c) { if (!isStepUpCancelled(c)) error.value = errMsg(c) } finally { busy.value = false }
}

onMounted(reloadAll)
</script>
