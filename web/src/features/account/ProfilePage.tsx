import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, useEffect, useState } from "react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { AccountLayout } from "@/features/account/AccountLayout";
import { ApiError, api, handleApiError, unwrap } from "@/lib/api";
import { authMeQueryOptions, metaQueryOptions } from "@/lib/queries";
import { queryKeys } from "@/lib/queryKeys";

export function ProfilePage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: meta } = useQuery(metaQueryOptions());
  const { data: me } = useQuery(authMeQueryOptions());

  const [name, setName] = useState("");
  const [nameError, setNameError] = useState<string | null>(null);

  useEffect(() => {
    if (meta && meta.mode !== "server") {
      void navigate({ to: "/account/preferences", replace: true });
    }
  }, [meta, navigate]);

  useEffect(() => {
    if (me?.user.name !== undefined) {
      setName(me.user.name);
    }
  }, [me?.user.name]);

  useEffect(() => {
    document.title = `Profile · ${meta?.instance_name || "Mailcubby"}`;
  }, [meta?.instance_name]);

  const updateMutation = useMutation({
    mutationFn: async (nextName: string) =>
      unwrap(
        await api.PATCH("/api/v1/auth/me", {
          body: { name: nextName },
        }),
      ),
    onSuccess: (updatedUser) => {
      setNameError(null);
      queryClient.setQueryData(queryKeys.authMe(), (prev: typeof me) =>
        prev ? { ...prev, user: updatedUser } : prev,
      );
      toast.success("Profile updated");
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 422 && err.details?.name) {
        setNameError(err.details.name);
        return;
      }
      handleApiError(err, "Failed to update profile");
    },
  });

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (trimmed.length < 1 || trimmed.length > 80) {
      setNameError("Name must be between 1 and 80 characters.");
      return;
    }
    setNameError(null);
    updateMutation.mutate(trimmed);
  };

  return (
    <AccountLayout
      section="profile"
      title="Profile"
      description="Manage your display name and view your account details."
    >
      <Card aria-labelledby="profile-heading">
        <CardHeader
          title="Account details"
          titleId="profile-heading"
          description="Your display name is shown to teammates in member lists."
        />
        <form onSubmit={handleSubmit} method="post" noValidate>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-col gap-1.5">
              <label htmlFor="profile-email" className="text-xs font-medium text-foreground">
                Email
              </label>
              <div className="flex items-center gap-2">
                <Input
                  id="profile-email"
                  type="email"
                  value={me?.user.email ?? ""}
                  readOnly
                  disabled
                  className="bg-muted/40 text-muted-foreground"
                />
                {me?.user.is_admin ? <Badge variant="accent">Admin</Badge> : null}
              </div>
              <p className="text-xs text-muted-foreground">
                Email addresses are managed by an instance administrator.
              </p>
            </div>

            <div className="flex flex-col gap-1.5">
              <label htmlFor="profile-name" className="text-xs font-medium text-foreground">
                Display name
              </label>
              <Input
                id="profile-name"
                name="name"
                type="text"
                autoComplete="name"
                maxLength={80}
                required
                value={name}
                onChange={(e) => {
                  setName(e.target.value);
                  if (nameError) {
                    setNameError(null);
                  }
                }}
                aria-invalid={nameError ? "true" : undefined}
                aria-describedby={nameError ? "profile-name-error" : undefined}
              />
              {nameError ? (
                <p id="profile-name-error" className="text-xs text-destructive">
                  {nameError}
                </p>
              ) : null}
            </div>
          </CardContent>
          <CardFooter>
            <Button type="submit" variant="primary" size="sm" disabled={updateMutation.isPending}>
              {updateMutation.isPending ? "Saving..." : "Save profile"}
            </Button>
          </CardFooter>
        </form>
      </Card>
    </AccountLayout>
  );
}
