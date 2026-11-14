export const cloudPages = [
  { section: 'overview', label: '概览', description: '查看节点容量、实例状态与需要关注的问题' },
  { section: 'nodes', label: '服务器节点', description: '管理服务器容量、分配状态和节点凭据' },
  { section: 'instances', label: '用户实例', description: '查看用户实例的归属、运行状态和付费到期时间' },
  { section: 'plan', label: '套餐与保留', description: '配置月付价格、开通开关和到期数据保留期' },
  { section: 'billing', label: '账单', description: '核对 Cloud 扣款、退款与账户余额' },
  { section: 'runtime-policy', label: '运行策略', description: '配置主控失联时已运行实例的容错窗口' }
] as const
export type CloudSection = typeof cloudPages[number]['section']
