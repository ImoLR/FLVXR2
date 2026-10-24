import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import {
  getLandingIntegration,
  saveLandingIntegration,
  testLandingIntegration,
  syncLandingIntegration,
  LandingIntegration,
  landingDate,
} from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Card, CardBody, CardHeader } from "@/shadcn-bridge/heroui/card";
import { Input } from "@/shadcn-bridge/heroui/input";

export function IntegrationSettings({ onSynced }: { onSynced: () => void }) {
  const [config, setConfig] = useState<LandingIntegration | null>(null);
  const [baseUrl, setBaseUrl] = useState("https://mmwxc.imgamer.top");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    void getLandingIntegration().then((res) => {
      if (res.code !== 0) {
        setError(res.msg);

        return;
      }
      setConfig(res.data);
      setBaseUrl(res.data.baseUrl);
    });
  }, []);
  const run = async (action: "save" | "test" | "sync") => {
    setBusy(action);
    setError("");
    try {
      if (action === "test") {
        const res = await testLandingIntegration({ baseUrl, token });

        if (res.code !== 0) setError(res.msg);
        else
          toast.success(
            "连接成功，共 " + res.data.shareCount + " 台共享服务器",
          );
      } else {
        const res =
          action === "save"
            ? await saveLandingIntegration({ baseUrl, token })
            : await syncLandingIntegration();

        if (res.code !== 0) {
          setError(res.msg);

          return;
        }
        setConfig(res.data);
        setToken("");
        onSynced();
        if (!res.data.syncError)
          toast.success(
            action === "save" ? "接入设置已保存并同步" : "共享服务器已同步",
          );
      }
    } finally {
      setBusy("");
    }
  };

  return (
    <Card>
      <CardHeader className="pb-2 md:pb-3">
        <h2 className="font-semibold">妙妙屋接入设置</h2>
      </CardHeader>
      <CardBody className="space-y-3">
        <div className="grid gap-3 md:grid-cols-2">
          <Input
            label="Custom 基础地址"
            value={baseUrl}
            variant="bordered"
            onChange={(event) => setBaseUrl(event.target.value)}
          />
          <Input
            autoComplete="new-password"
            label="接入令牌"
            placeholder={
              config?.hasToken
                ? "已保存；留空保持原令牌"
                : "填写 FLVX 专用接入令牌"
            }
            type="password"
            value={token}
            variant="bordered"
            onChange={(event) => setToken(event.target.value)}
          />
        </div>
        <p className="text-xs text-default-500">
          在妙妙屋「FLVX 共享」生成接入令牌。已保存的令牌不会回显。
        </p>
        <div className="space-y-1 text-sm">
          <p>
            最近成功同步：
            {config?.lastSyncAt ? landingDate(config.lastSyncAt) : "尚未同步"}
          </p>
          {(error || config?.syncError) && (
            <p
              className="rounded-lg bg-warning-50 p-2 text-warning"
              role="status"
            >
              同步异常 · {error || config?.syncError}
              。保留已有节点，连接恢复后自动重试。
            </p>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            color="primary"
            isDisabled={Boolean(busy)}
            isLoading={busy === "save"}
            size="sm"
            onPress={() => void run("save")}
          >
            保存并同步
          </Button>
          <Button
            color="secondary"
            isDisabled={Boolean(busy)}
            isLoading={busy === "test"}
            size="sm"
            variant="flat"
            onPress={() => void run("test")}
          >
            测试连接
          </Button>
          <Button
            color="success"
            isDisabled={Boolean(busy) || !config?.hasToken}
            isLoading={busy === "sync"}
            size="sm"
            variant="flat"
            onPress={() => void run("sync")}
          >
            立即同步
          </Button>
        </div>
      </CardBody>
    </Card>
  );
}
