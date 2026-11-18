import { useMemo, useState } from "react"
import { Link, Outlet, useLocation } from "react-router-dom"
import { useTheme } from "next-themes"
import { cn } from "@/lib/utils"
import { getCurrentRole } from "@/lib/auth"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import {
  ChevronRightIcon,
  FlaskConicalIcon,
  GraduationCapIcon,
  KeyRoundIcon,
  MenuIcon,
  MoonIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  PlusIcon,
  ShieldIcon,
  SunIcon,
} from "lucide-react"

const NAV_ITEMS = [
  { to: "/", label: "Исследования", icon: FlaskConicalIcon, end: true },
  { to: "/research/new", label: "Новое исследование", icon: PlusIcon, end: false },
]

const ADMIN_ITEM = { to: "/admin", label: "Админка", icon: ShieldIcon, end: false }

function useBreadcrumbs() {
  const { pathname } = useLocation()

  if (pathname === "/") {
    return [{ to: "/", label: "Исследования" }]
  }
  if (pathname === "/research/new") {
    return [
      { to: "/", label: "Исследования" },
      { to: "/research/new", label: "Новое исследование" },
    ]
  }
  if (pathname.startsWith("/research/")) {
    return [
      { to: "/", label: "Исследования" },
      { to: pathname, label: "Исследование" },
    ]
  }
  return [{ to: "/", label: "Исследования" }]
}

function ThemeToggle({ collapsed }: { collapsed: boolean }) {
  const { resolvedTheme, setTheme } = useTheme()
  const isDark = resolvedTheme === "dark"

  return (
    <Button
      variant="ghost"
      size="sm"
      className={cn("w-full justify-start gap-2.5 px-2.5", collapsed && "justify-center px-0")}
      onClick={() => setTheme(isDark ? "light" : "dark")}
    >
      {isDark ? <SunIcon className="size-4" /> : <MoonIcon className="size-4" />}
      {!collapsed && <span>{isDark ? "Светлая тема" : "Тёмная тема"}</span>}
    </Button>
  )
}

function SidebarNav({
  collapsed,
  onNavigate,
  isAdmin,
}: {
  collapsed: boolean
  onNavigate?: () => void
  isAdmin: boolean
}) {
  const { pathname } = useLocation()
  const items = isAdmin ? [...NAV_ITEMS, ADMIN_ITEM] : NAV_ITEMS

  return (
    <nav className="flex flex-col gap-1 px-3">
      {items.map((item) => {
        const Icon = item.icon
        const active = item.end ? pathname === item.to : pathname.startsWith(item.to)

        const link = (
          <Link
            to={item.to}
            onClick={onNavigate}
            className={cn(
              "flex items-center gap-3 rounded-lg px-2.5 py-2 text-sm font-medium transition-colors",
              active
                ? "bg-muted text-foreground"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
              collapsed && "justify-center px-0",
            )}
          >
            <Icon className="size-4 shrink-0" />
            {!collapsed && <span>{item.label}</span>}
          </Link>
        )

        if (!collapsed) return <div key={item.to}>{link}</div>

        return (
          <Tooltip key={item.to} delayDuration={0}>
            <TooltipTrigger asChild>{link}</TooltipTrigger>
            <TooltipContent side="right">{item.label}</TooltipContent>
          </Tooltip>
        )
      })}
    </nav>
  )
}

function SidebarContent({
  collapsed,
  onNavigate,
  isAdmin,
}: {
  collapsed: boolean
  onNavigate?: () => void
  isAdmin: boolean
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        className={cn(
          "flex items-center gap-2.5 py-5 pr-3",
          collapsed ? "justify-center pl-3" : "px-5",
        )}
      >
        <div className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground">
          <GraduationCapIcon className="size-4" />
        </div>
        {!collapsed && (
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold leading-tight">Activity Descriptor</p>
            <p className="text-xs text-muted-foreground">Анализ деятельности</p>
          </div>
        )}
      </div>

      <SidebarNav collapsed={collapsed} onNavigate={onNavigate} isAdmin={isAdmin} />

      <div className="mt-auto flex flex-col gap-1 border-t p-3">
        <ThemeToggle collapsed={collapsed} />
        <Button
          asChild
          variant="ghost"
          className={cn("w-full justify-start gap-2.5 px-2.5", collapsed && "justify-center px-0")}
        >
          <Link to="/login" onClick={onNavigate}>
            <KeyRoundIcon className="size-4" />
            {!collapsed && <span>Аккаунт</span>}
          </Link>
        </Button>
        {!collapsed && <p className="px-2.5 pt-1 text-xs text-muted-foreground">Версия 0.1.0</p>}
      </div>
    </div>
  )
}

export function Layout() {
  const [collapsed, setCollapsed] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const breadcrumbs = useBreadcrumbs()
  const { pathname } = useLocation()
  const isAdmin = useMemo(() => getCurrentRole() === "admin", [pathname])

  return (
    <TooltipProvider>
      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <div className="flex min-h-screen">
          {/* Desktop sidebar */}
          <aside
            className={cn(
              "sticky top-0 hidden h-screen shrink-0 flex-col border-r bg-card transition-[width] duration-150 lg:flex",
              collapsed ? "w-16" : "w-64",
            )}
          >
            <SidebarContent collapsed={collapsed} isAdmin={isAdmin} />
            <div className="border-t p-2">
              <Button
                variant="ghost"
                size="icon"
                className="w-full"
                onClick={() => setCollapsed((value) => !value)}
                aria-label={collapsed ? "Развернуть сайдбар" : "Свернуть сайдбар"}
              >
                {collapsed ? (
                  <PanelLeftOpenIcon className="size-4" />
                ) : (
                  <PanelLeftCloseIcon className="size-4" />
                )}
              </Button>
            </div>
          </aside>

          {/* Main column */}
          <div className="flex min-w-0 flex-1 flex-col">
            <header className="sticky top-0 z-10 flex h-14 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur lg:px-8">
              <SheetTrigger asChild>
                <Button variant="ghost" size="icon" className="lg:hidden" aria-label="Открыть меню">
                  <MenuIcon className="size-4" />
                </Button>
              </SheetTrigger>
              <nav className="flex items-center gap-1.5 text-sm">
                {breadcrumbs.map((crumb, index) => {
                  const isLast = index === breadcrumbs.length - 1
                  return (
                    <span key={crumb.to} className="flex items-center gap-1.5">
                      {index > 0 && <ChevronRightIcon className="size-3.5 text-muted-foreground" />}
                      {isLast ? (
                        <span className="font-medium text-foreground">{crumb.label}</span>
                      ) : (
                        <Link
                          to={crumb.to}
                          className="text-muted-foreground transition-colors hover:text-foreground"
                        >
                          {crumb.label}
                        </Link>
                      )}
                    </span>
                  )
                })}
              </nav>
            </header>

            <main className="mx-auto w-full max-w-6xl flex-1 space-y-6 px-4 py-8 lg:px-8">
              <Outlet />
            </main>
          </div>
        </div>

        <SheetContent side="left" className="w-72 p-0">
          <SheetTitle className="sr-only">Навигация</SheetTitle>
          <SidebarContent collapsed={false} isAdmin={isAdmin} onNavigate={() => setMobileOpen(false)} />
        </SheetContent>
      </Sheet>
    </TooltipProvider>
  )
}
