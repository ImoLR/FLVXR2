import { useEffect, useState } from "react";
import toast from "react-hot-toast";

import { getLandingSettings, saveLandingSettings } from "@/api/landing";
import { Button } from "@/shadcn-bridge/heroui/button";
import { Input } from "@/shadcn-bridge/heroui/input";
import {
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
} from "@/shadcn-bridge/heroui/modal";

// Plan 131: admin-only global landing settings (one value for now).
export function LandingGlobalSettings({ onClose }: { onClose: () => void }) {
  const [value, setValue] = useState("");
  const [customUsers, setCustomUsers] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let disposed = false;

    void getLandingSettings().then((res) => {
      if (disposed) return;
      if (res.code !== 0) {
        setError(res.msg);

        return;
      }
      setValue(String(res.data.defaultMaxNodes));
      setCustomUsers(res.data.customUserCount);
    });

    return () => {
      disposed = true;
    };
  }, []);

  const number = Number(value);
  const valid =
    value.trim() !== "" &&
    Number.isInteger(number) &&
    number >= 0 &&
    number <= 10000;

  const save = async () => {
    if (!valid) return;
    setSaving(true);
    try {
      const res = await saveLandingSettings(number);

      if (res.code !== 0) {
        toast.error(res.msg);

        return;
      }
      toast.success("落地全局设置已保存");
      onClose();
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal isOpen scrollBehavior="inside" onClose={onClose}>
      <ModalContent className="flvx-landing">
        <ModalHeader>落地全局设置</ModalHeader>
        <ModalBody className="space-y-4">
          {error && <p className="text-sm text-danger">{error}</p>}
          <Input
            description="未单独设置的用户都按这个数量；0 表示默认不能新建落地节点。调低不会删除或停用已有节点，只是不能再新建。"
            errorMessage={
              value.trim() !== "" && !valid
                ? "请输入 0–10000 的整数"
                : undefined
            }
            isInvalid={value.trim() !== "" && !valid}
            label="每个用户默认可建落地节点总数"
            min="0"
            type="number"
            value={value}
            variant="bordered"
            onChange={(event) => setValue(event.target.value)}
          />
          <p className="text-sm text-default-500">
            {customUsers === null
              ? "正在读取…"
              : customUsers > 0
                ? `有 ${customUsers} 个用户在用户编辑「落地」里单独设置了数量，不受此值影响。`
                : "目前没有用户单独设置数量，所有用户都跟随此值。"}
            管理员账号不受总数限制；每台服务器的「每人最多」仍然有效。
          </p>
        </ModalBody>
        <ModalFooter>
          <Button color="danger" variant="flat" onPress={onClose}>
            取消
          </Button>
          <Button
            color="primary"
            isDisabled={!valid}
            isLoading={saving}
            onPress={() => void save()}
          >
            保存
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  );
}
