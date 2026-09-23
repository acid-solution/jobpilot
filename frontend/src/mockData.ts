import type { Conversation, NavItem, PageId, PreparationStep } from './types'

export const currentTarget = '后端开发＋Agent 应用 · 2027 暑期实习'

export const navItems: NavItem[] = [
  { id: 'dashboard', shortLabel: '台', label: '工作台' },
  { id: 'market', shortLabel: '市', label: '市场画像' },
  { id: 'profile', shortLabel: '我', label: '用户画像' },
  { id: 'projects', shortLabel: '项', label: '项目推荐' },
  { id: 'gaps', shortLabel: '知', label: '知识短板' },
  { id: 'settings', shortLabel: '设', label: '设置' },
]

export const preparationSteps: PreparationStep[] = [
  {
    id: 'target',
    shortLabel: '✓',
    title: '求职目标',
    description: '方向和求职类型已经确定',
    state: '已完成',
    complete: true,
  },
  {
    id: 'market',
    shortLabel: 'JD',
    title: '市场画像',
    description: '8 份相关 JD 已完成，1 份正在处理',
    state: '8 / 10',
    complete: false,
  },
  {
    id: 'profile',
    shortLabel: '能',
    title: '用户画像',
    description: 'Redis、Agent 开发等 3 项能力待评估',
    state: '13 / 16',
    complete: false,
  },
]

export const nextStepPrompts: Record<PageId, string> = {
  dashboard:
    '根据当前进度，建议先再录入 2 份符合“后端开发＋Agent 应用”目标的完整 JD。市场画像达到门槛后，再补齐 Redis、Agent 开发和大模型基础 3 项能力。',
  market:
    '当前市场画像已完成 8 份相关 JD，建议先补充 2 份完整 JD，并优先选择同时包含后端职责和 Agent 开发职责的岗位。',
  profile:
    '当前还有 Redis、Agent 开发和大模型基础 3 项能力缺少判断依据，建议先用已有简历和经历自述补充；材料仍不足时再进行问答。',
  projects:
    '项目推荐需要先完成市场画像和用户画像。建议先补充 2 份相关 JD，再完成剩余 3 项能力评估。',
  gaps:
    '知识短板需要先完成市场画像和用户画像。建议先补充 2 份相关 JD，再完成剩余 3 项能力评估。',
  settings:
    '当前前端还未接入真实模型服务。页面功能完善后，可以在这里配置平台支持的模型。',
}

export const initialConversations: Conversation[] = [
  {
    id: 'jd-relevance',
    title: '为什么这份 JD 没计入',
    updatedLabel: '刚刚',
    messages: [
      {
        id: 'welcome',
        role: 'assistant',
        content: '你目前还差 2 份相关 JD，以及 3 项待评估能力。我建议先补齐市场画像。',
      },
      {
        id: 'question',
        role: 'user',
        content: '为什么 C 公司的 JD 没有计入？',
      },
      {
        id: 'answer',
        role: 'assistant',
        content:
          '它主要是普通业务后端，只把大模型经验列为加分项，没有实际的 Agent 开发职责，因此不满足当前复合目标。',
        toolNote: '已读取当前目标、JD 分类与原文依据 · 未修改资料',
      },
    ],
  },
  {
    id: 'complete-profile',
    title: '补齐用户画像',
    updatedLabel: '昨天 · 12 条消息',
    messages: [
      {
        id: 'profile-answer',
        role: 'assistant',
        content: '当前还有 Redis、Agent 开发和大模型基础 3 项能力需要补充判断依据。',
      },
    ],
  },
  {
    id: 'mysql-level',
    title: 'MySQL 等级解释',
    updatedLabel: '9 月 8 日 · 8 条消息',
    messages: [
      {
        id: 'mysql-answer',
        role: 'assistant',
        content: '当前记录为 MySQL L2。这个结果使用综合能力等级，不会拆成索引、事务等多个分数。',
      },
    ],
  },
]
