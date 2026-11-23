import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import toast from "react-hot-toast";
import { Turnstile } from "@marsidev/react-turnstile";
import { motion } from "framer-motion";

import { Input } from "@/shadcn-bridge/heroui/input";
import { Button } from "@/shadcn-bridge/heroui/button";
import {
  Modal,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
} from "@/shadcn-bridge/heroui/modal";
import {
  invalidateCachedConfigs,
  siteConfig,
  updateSiteConfig,
} from "@/config/site";
import { BrandLogo } from "@/components/brand-logo";
import { BrandName } from "@/components/brand-mark";
import { VersionFooter } from "@/components/version-footer";
import { useSiteName } from "@/hooks/useSiteName";
import DefaultLayout from "@/layouts/default";
import { login, register, LoginData, getConfigByName } from "@/api";
import { writeLoginSession } from "@/utils/session";
import { useWebViewMode } from "@/hooks/useWebViewMode";
import { resetSubLinkSwitchCheck } from "@/api/sub-link";
import { checkFrontendBuild } from "@/utils/build-freshness";
import { prefetchOnIdle } from "@/utils/idle-prefetch";
import { APP_HOME, isAppMode } from "@/utils/app-bridge";

interface LoginForm {
  username: string;
  password: string;
}

export default function IndexPage() {
  const [form, setForm] = useState<LoginForm>({
    username: "",
    password: "",
  });
  const [errors, setErrors] = useState<Partial<LoginForm>>({});
  const [loading, setLoading] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const siteName = useSiteName();
  const isWebView = useWebViewMode();
  const navigate = useNavigate();
  const [siteKey, setSiteKey] = useState("");
  const [regSiteKey, setRegSiteKey] = useState("");

  const [registerOpen, setRegisterOpen] = useState(false);
  const [registerForm, setRegisterForm] = useState({
    user: "",
    password: "",
    confirm: "",
  });
  const [registerErrors, setRegisterErrors] = useState<
    Partial<typeof registerForm>
  >({});
  const [registerLoading, setRegisterLoading] = useState(false);
  const [regEnabled, setRegEnabled] = useState(true);

  const [captchaEnabled, setCaptchaEnabled] = useState(false);
  const [showLoginCaptcha, setShowLoginCaptcha] = useState(false);
  const [loginCaptchaToken, setLoginCaptchaToken] = useState("");

  const [regCaptchaEnabled, setRegCaptchaEnabled] = useState(false);
  const [showRegCaptcha, setShowRegCaptcha] = useState(false);
  const [regCaptchaId, setRegCaptchaId] = useState("");

  // Plan 209: while the visitor types, fetch the 主页 chunks so the page after
  // login renders at once (idle time, once; skipped on Data Saver / 2G).
  useEffect(() => {
    prefetchOnIdle("after-login-page");
  }, []);

  useEffect(() => {
    getConfigByName("registration_enabled")
      .then((res) => {
        if (res.code === 0 && res.data) {
          const v = res.data.value;

          setRegEnabled(v === "1" || v === "true");
        }
      })
      .catch(() => {});
    getConfigByName("register_captcha_enabled")
      .then((res) => {
        if (res.code === 0 && res.data) {
          setRegCaptchaEnabled(res.data.value === "true");
        }
      })
      .catch(() => {});
    getConfigByName("captcha_enabled")
      .then((res) => {
        if (res.code === 0 && res.data) {
          setCaptchaEnabled(res.data.value === "true");
        }
      })
      .catch(() => {});
    getConfigByName("cloudflare_site_key")
      .then((res) => {
        if (res.code === 0 && res.data && res.data.value) {
          setSiteKey(res.data.value);
        }
      })
      .catch(() => {});
  }, []);

  const validateForm = (): boolean => {
    const newErrors: Partial<LoginForm> = {};

    if (!form.username.trim()) {
      newErrors.username = "请输入用户名";
    }
    if (!form.password.trim()) {
      newErrors.password = "请输入密码";
    } else if (form.password.length < 6) {
      newErrors.password = "密码长度至少6位";
    }
    setErrors(newErrors);

    return Object.keys(newErrors).length === 0;
  };

  const handleInputChange = (field: keyof LoginForm, value: string) => {
    setForm((prev) => ({ ...prev, [field]: value }));
    if (errors[field]) {
      setErrors((prev) => ({ ...prev, [field]: undefined }));
    }
  };

  const performLogin = async (captchaToken?: string) => {
    try {
      const loginData: LoginData = {
        username: form.username.trim(),
        password: form.password,
        captchaId: captchaToken || loginCaptchaToken || "",
      };

      const response = await login(loginData);

      if (response.code !== 0) {
        toast.error(response.msg || "登录失败");
        if (captchaEnabled && siteKey) {
          setLoginCaptchaToken("");
        }

        return;
      }

      if (response.data.requirePasswordChange) {
        writeLoginSession(response.data);
        toast.success("检测到默认密码，即将跳转到修改密码页面");
        window.location.href = "/change-password";

        return;
      }

      writeLoginSession(response.data);
      toast.success("登录成功");
      // Plan 209: open 主页 in-app — the app is already loaded, so a full page
      // load would only download and start it again. Redo what that fresh
      // start did: signed-in config, the per-load sub-link switch check and
      // the frontend build check (it reloads onto a newer build, token kept).
      invalidateCachedConfigs();
      resetSubLinkSwitchCheck();
      void updateSiteConfig();
      navigate(isAppMode() ? APP_HOME : "/dashboard");
      void checkFrontendBuild();
    } catch {
      toast.error("网络错误，请稍后重试");
    } finally {
      setLoading(false);
    }
  };

  const handleLogin = async () => {
    if (!validateForm()) return;

    if (captchaEnabled && siteKey && !loginCaptchaToken) {
      setShowLoginCaptcha(true);

      return;
    }

    setLoading(true);
    await performLogin();
  };

  const handleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !loading) {
      handleLogin();
    }
  };

  const performRegister = async (captchaToken?: string) => {
    setRegisterLoading(true);
    try {
      const res = await register({
        user: registerForm.user.trim(),
        password: registerForm.password,
        captchaId: captchaToken || regCaptchaId || "",
      });

      if (res.code === 0) {
        writeLoginSession(res.data);
        toast.success("注册成功");
        setRegisterOpen(false);
        setRegCaptchaId("");
        window.location.href = "/dashboard";
      } else {
        toast.error(res.msg || "注册失败");
      }
    } catch {
      toast.error("网络错误");
    } finally {
      setRegisterLoading(false);
    }
  };

  const handleRegister = async () => {
    const errs: Partial<typeof registerForm> = {};

    if (!registerForm.user.trim()) errs.user = "请输入用户名";
    if (!registerForm.password) errs.password = "请输入密码";
    else if (registerForm.password.length < 6)
      errs.password = "密码长度至少6位";
    if (registerForm.password !== registerForm.confirm)
      errs.confirm = "两次密码不一致";
    setRegisterErrors(errs);
    if (Object.keys(errs).length > 0) return;

    if (regCaptchaEnabled && regSiteKey && !regCaptchaId) {
      setShowRegCaptcha(true);

      return;
    }

    await performRegister();
  };

  const getTurnstileTheme = (): "light" | "dark" | "auto" => {
    if (
      document.documentElement.classList.contains("dark") ||
      document.documentElement.getAttribute("data-theme") === "dark" ||
      window.matchMedia("(prefers-color-scheme: dark)").matches
    ) {
      return "dark";
    }

    return "light";
  };

  return (
    <DefaultLayout
      className="imgamer-login-bg"
      mainClassName="flex flex-col pt-0 sm:pt-0"
    >
      {/* Plan 292: im.gamer login (tag chips in the fields, soft inputs). */}
      <section className="flex flex-1 flex-col items-center min-h-[calc(100dvh-60px)]">
        <div className="flex w-full flex-1 items-center justify-center py-5 sm:py-8">
          <motion.div
            animate={{ opacity: 1, y: 0 }}
            className="w-full max-w-[460px]"
            initial={{ opacity: 0, y: 24 }}
            transition={{ duration: 0.35, ease: [0.25, 0.46, 0.45, 0.94] }}
          >
            <div className="imgamer-login-card">
              <div className="text-center">
                <BrandLogo className="imgamer-login-logo" size={76} />
                <BrandName
                  className="brand-ink text-[22px]"
                  name={siteName}
                  textClassName="font-bold"
                />
                <h1 className="imgamer-login-title">登陆</h1>
                <p className="imgamer-login-sub">请输入您的账号信息</p>
                <div className="mt-3.5 mb-[22px] flex flex-wrap justify-center gap-1.5">
                  <span className="imgamer-chip imgamer-chip-brand">
                    🎮 玩家专线
                  </span>
                  <span className="imgamer-chip imgamer-chip-green">
                    {"\u26A1\uFE0F"} 低延迟
                  </span>
                  <span className="imgamer-chip imgamer-chip-purple">
                    🛡️ 稳定转发
                  </span>
                </div>
              </div>

              <label
                className="imgamer-field mb-3.5"
                data-disabled={loading || undefined}
                data-invalid={!!errors.username || undefined}
              >
                <span className="imgamer-chip imgamer-chip-brand">👤 账号</span>
                <input
                  aria-invalid={!!errors.username}
                  aria-label="用户名"
                  autoCapitalize="none"
                  autoComplete="username"
                  disabled={loading}
                  name="username"
                  placeholder="请输入用户名"
                  spellCheck={false}
                  value={form.username}
                  onChange={(e) =>
                    handleInputChange("username", e.target.value)
                  }
                  onKeyDown={handleKeyPress}
                />
              </label>
              {errors.username ? (
                <p className="imgamer-field-error" role="alert">
                  {errors.username}
                </p>
              ) : null}

              <label
                className="imgamer-field mb-3.5"
                data-disabled={loading || undefined}
                data-invalid={!!errors.password || undefined}
              >
                <span className="imgamer-chip imgamer-chip-amber">🔒 密码</span>
                <input
                  aria-invalid={!!errors.password}
                  aria-label="密码"
                  autoComplete="current-password"
                  disabled={loading}
                  name="password"
                  placeholder="请输入密码"
                  type={showPassword ? "text" : "password"}
                  value={form.password}
                  onChange={(e) =>
                    handleInputChange("password", e.target.value)
                  }
                  onKeyDown={handleKeyPress}
                />
                <button
                  aria-label={showPassword ? "隐藏密码" : "显示密码"}
                  aria-pressed={showPassword}
                  className="imgamer-eye"
                  title={showPassword ? "隐藏密码" : "显示密码"}
                  type="button"
                  onClick={() => setShowPassword((v) => !v)}
                >
                  {showPassword ? (
                    <svg
                      fill="none"
                      height="18"
                      stroke="currentColor"
                      strokeLinecap="round"
                      strokeWidth="2"
                      viewBox="0 0 24 24"
                      width="18"
                    >
                      <path d="M3 3l18 18" />
                      <path d="M10.6 5.1A10.4 10.4 0 0 1 12 5c6.4 0 10 7 10 7a17.6 17.6 0 0 1-3.1 3.9M6.6 6.6C3.8 8.4 2 12 2 12s3.6 7 10 7c1.8 0 3.4-.5 4.7-1.2" />
                      <path d="M9.9 9.9a3 3 0 0 0 4.2 4.2" />
                    </svg>
                  ) : (
                    <svg
                      fill="none"
                      height="18"
                      stroke="currentColor"
                      strokeWidth="2"
                      viewBox="0 0 24 24"
                      width="18"
                    >
                      <path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12Z" />
                      <circle cx="12" cy="12" r="3" />
                    </svg>
                  )}
                </button>
              </label>
              {errors.password ? (
                <p className="imgamer-field-error" role="alert">
                  {errors.password}
                </p>
              ) : null}

              <button
                className="imgamer-login-button"
                disabled={loading}
                type="button"
                onClick={() => void handleLogin()}
              >
                {loading ? (
                  <>
                    <span aria-hidden="true" className="imgamer-spinner" />
                    登录中...
                  </>
                ) : (
                  "登录"
                )}
              </button>
              {captchaEnabled && siteKey && showLoginCaptcha && (
                <div className="flex justify-center pt-4">
                  <Turnstile
                    options={{ theme: getTurnstileTheme() }}
                    siteKey={siteKey}
                    onError={() => {
                      toast.error("验证失败，请刷新重试");
                      setLoginCaptchaToken("");
                    }}
                    onExpire={() => setLoginCaptchaToken("")}
                    onSuccess={(token) => {
                      setLoginCaptchaToken(token);
                      setLoading(true);
                      void performLogin(token);
                    }}
                  />
                </div>
              )}
              {regEnabled && (
                <div className="text-center mt-4">
                  <button
                    className="imgamer-login-link"
                    type="button"
                    onClick={() => setRegisterOpen(true)}
                  >
                    没有账号？立即注册
                  </button>
                </div>
              )}
            </div>
          </motion.div>
        </div>

        <VersionFooter
          containerClassName="w-full text-center py-4"
          poweredClassName="text-[13px] text-slate-600 dark:text-slate-300"
          showUpdateInfo={false}
          updateBadgeClassName="inline-flex items-center h-[18px] px-1.5 rounded-sm bg-green-500/90 text-[10px] font-semibold text-white"
          version={isWebView ? siteConfig.app_version : siteConfig.version}
          versionClassName="text-[13px] text-slate-600 dark:text-slate-400"
        />

        <Modal
          isOpen={registerOpen}
          placement="center"
          onOpenChange={(open) => {
            if (!open) {
              setRegisterOpen(false);
              setRegisterForm({ user: "", password: "", confirm: "" });
              setRegisterErrors({});
              setRegCaptchaId("");
              setRegSiteKey("");
            }
          }}
        >
          <ModalContent>
            <ModalHeader>注册账号</ModalHeader>
            <ModalBody>
              <div className="flex flex-col gap-4">
                <Input
                  errorMessage={registerErrors.user}
                  isInvalid={!!registerErrors.user}
                  label="用户名"
                  placeholder="请输入用户名"
                  value={registerForm.user}
                  variant="bordered"
                  onChange={(e) =>
                    setRegisterForm((p) => ({ ...p, user: e.target.value }))
                  }
                />
                <Input
                  errorMessage={registerErrors.password}
                  isInvalid={!!registerErrors.password}
                  label="密码"
                  placeholder="请输入密码"
                  type="password"
                  value={registerForm.password}
                  variant="bordered"
                  onChange={(e) =>
                    setRegisterForm((p) => ({ ...p, password: e.target.value }))
                  }
                />
                <Input
                  errorMessage={registerErrors.confirm}
                  isInvalid={!!registerErrors.confirm}
                  label="确认密码"
                  placeholder="再次输入密码"
                  type="password"
                  value={registerForm.confirm}
                  variant="bordered"
                  onChange={(e) =>
                    setRegisterForm((p) => ({ ...p, confirm: e.target.value }))
                  }
                />
                {regCaptchaEnabled && regSiteKey && showRegCaptcha && (
                  <div className="flex justify-center py-2">
                    <Turnstile
                      options={{ theme: getTurnstileTheme() }}
                      siteKey={regSiteKey}
                      onError={() => {
                        toast.error("验证失败，请刷新重试");
                        setRegCaptchaId("");
                      }}
                      onExpire={() => setRegCaptchaId("")}
                      onSuccess={(token) => {
                        setRegCaptchaId(token);
                        setRegisterLoading(true);
                        void performRegister(token);
                      }}
                    />
                  </div>
                )}
              </div>
            </ModalBody>
            <ModalFooter>
              <Button
                variant="flat"
                onPress={() => {
                  setRegisterOpen(false);
                  setRegisterForm({ user: "", password: "", confirm: "" });
                  setRegisterErrors({});
                  setRegCaptchaId("");
                  setRegSiteKey("");
                }}
              >
                取消
              </Button>
              <Button
                color="primary"
                isLoading={registerLoading}
                onPress={handleRegister}
              >
                注册
              </Button>
            </ModalFooter>
          </ModalContent>
        </Modal>
      </section>
    </DefaultLayout>
  );
}
