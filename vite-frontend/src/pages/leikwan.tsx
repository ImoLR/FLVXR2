import { AnimatedPage } from "@/components/animated-page";
import { LeiKwanAdminView } from "@/pages/leikwan/panel/admin-page";
import { LeiKwanUserView } from "@/pages/leikwan/panel/user-page";
import { isAdmin } from "@/utils/auth";

// Plan 270: 利群汇聚 = everything 利群 on one page. Admins get the full view
// (专线 · 规则 tree, 授权, 入口机, 旧管道, 设置); users with an active 利群 grant
// get their own 利群 rules (users without a grant are sent back to 主页).
// The old 账号设置 / 服务概览 forward table / pipe create + takeover UI and the
// 「导入为规则」 tab are gone (their APIs stay).

export default function LeiKwanPage() {
  return (
    <AnimatedPage className="px-3 py-6 lg:px-7 lg:py-6">
      {isAdmin() ? <LeiKwanAdminView /> : <LeiKwanUserView />}
    </AnimatedPage>
  );
}
