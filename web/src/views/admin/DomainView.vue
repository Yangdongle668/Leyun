<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Lock, Refresh, WarnTriangleFilled } from '@element-plus/icons-vue'

import { api, type TLSStatus } from '@/api'

const loading = ref(true)
const saving = ref(false)
const issuing = ref(false)
const status = ref<TLSStatus>()

const form = reactive({
  enabled: false,
  domains: '',
  email: '',
  directory_url: '',
  redirect: true,
  agree_tos: false,
})

const LE_STAGING = 'https://acme-staging-v02.api.letsencrypt.org/directory'

const useStaging = computed({
  get: () => form.directory_url === LE_STAGING,
  set: (v: boolean) => {
    form.directory_url = v ? LE_STAGING : ''
  },
})

onMounted(load)

async function load() {
  loading.value = true
  try {
    const res = await api.tlsSettings()
    status.value = res.status
    form.enabled = res.status.enabled
    form.domains = res.raw
    form.email = res.status.email
    form.redirect = res.status.redirect
    form.agree_tos = res.status.agreed
    form.directory_url = res.status.staging ? LE_STAGING : ''
  } catch (err) {
    ElMessage.error((err as Error).message || '读取配置失败')
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const res = await api.updateTLSSettings({
      enabled: form.enabled,
      domains: form.domains,
      email: form.email,
      directory_url: form.directory_url,
      redirect: form.redirect,
      agree_tos: form.agree_tos,
    })
    status.value = res.status
    ElMessage.success('已保存')
    if (res.restart_required) {
      // 监听 443 要在进程启动时建立，不说清楚管理员会纳闷为什么还是 http。
      await ElMessageBox.alert(res.restart_hint, '还差一步', { confirmButtonText: '知道了' })
    }
  } catch (err) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function issue() {
  issuing.value = true
  try {
    const res = await api.issueTLSCert()
    status.value = res.status
    const ok = res.certs.filter((c) => c.issued).length
    if (ok === res.certs.length) {
      ElMessage.success(`${ok} 张证书已就绪`)
    } else {
      ElMessage.warning(`${ok}/${res.certs.length} 张成功，失败原因见下表`)
    }
  } catch (err) {
    ElMessage.error((err as Error).message || '申请失败')
  } finally {
    issuing.value = false
  }
}

function expiryType(days: number) {
  if (days <= 7) return 'danger'
  if (days <= 21) return 'warning'
  return 'success'
}

function fmtDate(s?: string) {
  if (!s) return '—'
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}
</script>

<template>
  <div class="ly-page" v-loading="loading">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">域名与 HTTPS</h1>
        <p class="ly-page-desc">
          绑定域名后自动向 Let's Encrypt 申请免费证书，并在到期前自动续期，不需要人工干预。
        </p>
      </div>
    </div>

    <div class="ly-card ly-card-pad ly-tls-note">
      <el-icon :size="18" color="#b5741a"><WarnTriangleFilled /></el-icon>
      <div>
        <p><strong>动手之前先确认这两件事，否则一定申请不下来：</strong></p>
        <p>
          1. 域名的 A 记录已经解析到这台服务器的<strong>公网 IP</strong>，并且已经生效
          （用 <code>ping 你的域名</code> 能看到正确的 IP）。
        </p>
        <p>
          2. 服务器的 <strong>80 和 443 端口对公网开放</strong>——云服务器要同时放开
          安全组和系统防火墙。证书签发机构要主动访问这两个端口来验证域名归属。
        </p>
        <p>
          如果乐云跑在 Nginx 之类的反向代理后面，证书通常由代理来管，这一页可以不用开。
        </p>
      </div>
    </div>

    <div class="ly-card ly-card-pad">
      <h2 class="ly-section-title">绑定域名</h2>
      <el-form label-width="130px" label-position="left">
        <el-form-item label="域名" required>
          <el-input
            v-model="form.domains"
            type="textarea"
            :rows="3"
            placeholder="pan.example.com&#10;drive.example.com"
          />
          <div class="ly-hint">
            一行一个，也可以用逗号分隔。只会为这里列出的域名签发证书。
            不支持 IP 和 .local / .internal 这类内网域名——签发机构不给它们发证书。
          </div>
        </el-form-item>

        <el-form-item label="联系邮箱">
          <el-input v-model="form.email" placeholder="admin@example.com" />
          <div class="ly-hint">证书将到期而续期又一直失败时，签发机构会往这个邮箱发提醒。建议填。</div>
        </el-form-item>

        <el-form-item label="HTTP 跳 HTTPS">
          <el-switch v-model="form.redirect" />
          <span class="ly-hint" style="margin-left: 10px">开启后访问 http:// 会自动跳到 https://</span>
        </el-form-item>

        <el-form-item label="先用测试环境">
          <el-switch v-model="useStaging" />
          <span class="ly-hint" style="margin-left: 10px">
            调试解析和端口时建议打开：正式环境每周的签发次数有限额，试错几次就可能被锁一星期。
            测试环境签出来的证书浏览器不认，调通后关掉再申请一次即可。
          </span>
        </el-form-item>

        <el-form-item label="服务条款">
          <el-checkbox v-model="form.agree_tos">
            我已阅读并同意 Let's Encrypt 的订阅者协议
          </el-checkbox>
          <div class="ly-hint">ACME 协议要求明示同意，没勾选就不能申请。</div>
        </el-form-item>

        <el-form-item label="启用 HTTPS">
          <el-switch v-model="form.enabled" :disabled="!form.agree_tos || !form.domains.trim()" />
          <span class="ly-hint" style="margin-left: 10px">需要先填好域名并勾选服务条款</span>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">保存</el-button>
          <el-button :icon="Lock" :loading="issuing" :disabled="!status?.enabled" @click="issue">
            立即申请 / 续期
          </el-button>
          <el-button :icon="Refresh" @click="load">刷新状态</el-button>
        </el-form-item>
      </el-form>
    </div>

    <div class="ly-card ly-card-pad">
      <h2 class="ly-section-title">证书状态</h2>

      <div v-if="status?.last_err" class="ly-tls-err">
        <el-icon><WarnTriangleFilled /></el-icon>
        <span>最近一次申请失败：{{ status.last_err }}</span>
      </div>

      <el-table v-if="status?.certs?.length" :data="status.certs" size="small">
        <el-table-column prop="domain" label="域名" min-width="180" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag v-if="row.issued" type="success" size="small">已签发</el-tag>
            <el-tag v-else type="info" size="small">未签发</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="issuer" label="签发机构" width="180" show-overflow-tooltip />
        <el-table-column label="到期时间" width="180">
          <template #default="{ row }">{{ fmtDate(row.not_after) }}</template>
        </el-table-column>
        <el-table-column label="剩余" width="110">
          <template #default="{ row }">
            <el-tag v-if="row.issued" :type="expiryType(row.days_left)" size="small">
              {{ row.days_left }} 天
            </el-tag>
            <span v-else class="ly-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column prop="err" label="问题" min-width="220" show-overflow-tooltip />
      </el-table>

      <div v-else class="ly-empty">
        <span class="ly-muted">还没有绑定域名</span>
      </div>

      <p class="ly-hint" style="margin-top: 12px">
        证书有效期 90 天，系统会在到期前 30 天自动续期，无需人工操作。
        上面的"立即申请"只是在想马上确认结果时用。
      </p>
    </div>
  </div>
</template>

<style scoped>
.ly-tls-note {
  display: flex;
  gap: 12px;
  margin-bottom: 18px;
  background: #fff8e8;
  border-color: #f6e3bd;
}
.ly-tls-note p {
  margin: 0 0 4px;
  font-size: 13px;
  color: #8a6316;
  line-height: 1.8;
}
.ly-tls-note .el-icon {
  flex-shrink: 0;
  margin-top: 3px;
}
.ly-tls-note code {
  background: rgba(0, 0, 0, 0.06);
  padding: 1px 5px;
  border-radius: 4px;
}
.ly-section-title {
  font-size: 15px;
  margin: 0 0 16px;
}
.ly-card-pad + .ly-card-pad {
  margin-top: 16px;
}
.ly-tls-err {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 12px;
  margin-bottom: 14px;
  border-radius: 8px;
  background: #fef0f0;
  border: 1px solid #fbc4c4;
  color: #c04646;
  font-size: 13px;
  line-height: 1.7;
}
.ly-tls-err .el-icon {
  flex-shrink: 0;
  margin-top: 3px;
}
</style>
