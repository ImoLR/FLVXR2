import {
  Navigate,
  Route,
  Routes,
  useNavigate,
  useLocation,
} from "react-router-dom";
import { Suspense, lazy, useEffect, useRef } from "react";

import IndexPage from "@/pages/index";
import AdminLayout from "@/layouts/admin";
import H5Layout from "@/layouts/h5";
import { isLoggedIn } from "@/utils/auth";
import { isRestricted } from "@/utils/session";
import {
  invalidateCachedConfigs,
  siteConfig,
  updateSiteConfig,
} from "@/config/site";
import { useH5Mode } from "@/hooks/useH5Mode";
import { pages } from "@/routes/pages";
import { FlowChartPlot } from "@/pages/dashboard/flow-chart-lazy";
import { definePrefetch, pausePrefetch } from "@/utils/idle-prefetch";

// Plan 209: every page except the login page is its own chunk (see
// routes/page-loaders.ts); the layout stays mounted while a page loads.
const {
  changePassword: ChangePasswordPage,
  dashboard: DashboardPage,
  monitor: MonitorPage,
  forward: ForwardPage,
  tunnel: TunnelPage,
  leikwan: LeiKwanPage,
  landing: LandingPage,
  subLink: SubLinkPage,
  node: NodePage,
  nodeMonitor: NodeMonitorPage,
  tls: TLSPage,
  user: UserPage,
  group: GroupPage,
  profile: ProfilePage,
  config: ConfigPage,
  panelSharing: PanelSharingPage,
  shop: ShopPage,
  myhome: MyHomePage,
  adminPlans: AdminPlansPage,
  adminOrders: AdminOrdersPage,
  adminPayment: AdminPaymentPage,
} = pages;

// Plan 209 idle prefetch: the login page fetches 主页 (and its chart) while
// the visitor types; 主页 fetches the pages opened next.
definePrefetch("after-login-page", [
  DashboardPage.preload,
  FlowChartPlot.preload,
]);
definePrefetch("after-dashboard", [
  ForwardPage.preload,
  TunnelPage.preload,
  LandingPage.preload,
]);

// Plan 187: the public probe page is its own chunk (main.tsx loads it directly
// for a fresh /tz visit; this route covers in-app navigation to /tz).
const ProbeApp = lazy(() => import("@/probe/probe-app"));

const RESTRICTED_PATHS = ["/myhome", "/shop", "/dashboard"];

// 简化的路由保护组件 - 使用 React Router 导航避免循环
const ProtectedRoute = ({
  children,
  skipLayout = false,
}: {
  children: React.ReactNode;
  skipLayout?: boolean;
}) => {
  const authenticated = isLoggedIn();
  const restricted = isRestricted();
  const isH5 = useH5Mode();
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    if (!authenticated) {
      navigate("/", { replace: true });
    } else if (restricted && !RESTRICTED_PATHS.includes(location.pathname)) {
      navigate("/myhome", { replace: true });
    }
  }, [authenticated, restricted, location.pathname, navigate]);

  if (!authenticated) {
    return (
      <div className="flex items-center justify-center min-h-screen bg-white dark:bg-black">
        <div className="text-lg text-gray-700 dark:text-gray-200" />
      </div>
    );
  }

  const page = <Suspense fallback={null}>{children}</Suspense>;

  if (skipLayout) {
    return page;
  }

  const Layout = isH5 ? H5Layout : AdminLayout;

  return <Layout>{page}</Layout>;
};

// 登录页面路由组件 - 已登录则重定向到dashboard（或 sessionStorage 中的重定向地址）
const LoginRoute = () => {
  const authenticated = isLoggedIn();
  const restricted = isRestricted();
  const navigate = useNavigate();

  useEffect(() => {
    if (authenticated) {
      const redirect = sessionStorage.getItem("login_redirect");

      if (redirect) {
        sessionStorage.removeItem("login_redirect");
        navigate(redirect, { replace: true });
      } else if (restricted) {
        navigate("/myhome", { replace: true });
      } else {
        navigate("/dashboard", { replace: true });
      }
    }
  }, [authenticated, restricted, navigate]);

  if (authenticated) {
    return (
      <div className="flex items-center justify-center min-h-screen bg-gray-100 dark:bg-black">
        <div className="text-lg text-gray-700 dark:text-gray-200" />
      </div>
    );
  }

  return <IndexPage />;
};

function App() {
  const { pathname } = useLocation();
  const firstPath = useRef(true);

  // Plan 209: a page the visitor just opened gets the network first; queued
  // idle prefetches wait a moment (not on the first page of a load).
  useEffect(() => {
    if (firstPath.current) {
      firstPath.current = false;

      return;
    }
    pausePrefetch();
  }, [pathname]);

  // 立即设置页面标题（使用已从缓存读取的配置）
  useEffect(() => {
    document.title = siteConfig.name;

    void updateSiteConfig();

    const handleConfigUpdate = () => {
      invalidateCachedConfigs();
      void updateSiteConfig();
    };

    window.addEventListener("configUpdated", handleConfigUpdate);

    return () => {
      window.removeEventListener("configUpdated", handleConfigUpdate);
    };
  }, []);

  return (
    <Routes>
      <Route element={<LoginRoute />} path="/" />
      <Route
        element={
          <Suspense fallback={null}>
            <ProbeApp />
          </Suspense>
        }
        path="/tz/*"
      />
      <Route
        element={
          <ProtectedRoute skipLayout={true}>
            <ChangePasswordPage />
          </ProtectedRoute>
        }
        path="/change-password"
      />
      <Route
        element={
          <ProtectedRoute>
            <DashboardPage />
          </ProtectedRoute>
        }
        path="/dashboard"
      />
      <Route
        element={
          <ProtectedRoute>
            <MonitorPage />
          </ProtectedRoute>
        }
        path="/monitor"
      />
      <Route
        element={
          <ProtectedRoute>
            <ForwardPage />
          </ProtectedRoute>
        }
        path="/forward"
      />
      <Route
        element={
          <ProtectedRoute>
            <TunnelPage />
          </ProtectedRoute>
        }
        path="/tunnel"
      />
      <Route
        element={
          <ProtectedRoute>
            <LeiKwanPage />
          </ProtectedRoute>
        }
        path="/leikwan"
      />
      <Route
        element={
          <ProtectedRoute>
            <LandingPage />
          </ProtectedRoute>
        }
        path="/landing"
      />
      <Route
        element={
          <ProtectedRoute>
            <SubLinkPage />
          </ProtectedRoute>
        }
        path="/sub"
      />
      <Route
        element={
          <ProtectedRoute>
            <NodePage />
          </ProtectedRoute>
        }
        path="/node"
      />
      <Route
        element={
          <ProtectedRoute>
            <NodeMonitorPage />
          </ProtectedRoute>
        }
        path="/node-monitor"
      />
      <Route
        element={
          <ProtectedRoute>
            <TLSPage />
          </ProtectedRoute>
        }
        path="/tls"
      />
      <Route
        element={
          <ProtectedRoute>
            <UserPage />
          </ProtectedRoute>
        }
        path="/user"
      />
      <Route
        element={
          <ProtectedRoute>
            <GroupPage />
          </ProtectedRoute>
        }
        path="/group"
      />
      <Route
        element={
          <ProtectedRoute>
            <ProfilePage />
          </ProtectedRoute>
        }
        path="/profile"
      />
      {/* Plan 170: 限速规则 retired (speed = 编辑用户 → 默认速率 + 服务器速率). */}
      <Route element={<Navigate replace to="/dashboard" />} path="/limit" />
      <Route
        element={
          <ProtectedRoute>
            <ConfigPage />
          </ProtectedRoute>
        }
        path="/config"
      />
      <Route
        element={
          <ProtectedRoute>
            <PanelSharingPage />
          </ProtectedRoute>
        }
        path="/panel-sharing"
      />
      <Route
        element={
          <ProtectedRoute>
            <ShopPage />
          </ProtectedRoute>
        }
        path="/shop"
      />
      <Route
        element={
          <ProtectedRoute>
            <MyHomePage />
          </ProtectedRoute>
        }
        path="/myhome"
      />
      <Route
        element={
          <ProtectedRoute>
            <AdminPlansPage />
          </ProtectedRoute>
        }
        path="/admin/plans"
      />
      <Route
        element={
          <ProtectedRoute>
            <AdminOrdersPage />
          </ProtectedRoute>
        }
        path="/admin/orders"
      />
      <Route
        element={
          <ProtectedRoute>
            <AdminPaymentPage />
          </ProtectedRoute>
        }
        path="/admin/payment"
      />
    </Routes>
  );
}

export default App;
