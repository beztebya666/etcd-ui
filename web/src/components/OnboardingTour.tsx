import { useEffect, useState } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { useStore } from "../lib/store";
import { useFocusTrap } from "../lib/focusTrap";
import {
  LayoutDashboard,
  FolderTree,
  Radio,
  Workflow,
  Users,
  ScrollText,
  Wrench,
  Sparkles,
  Command,
  Lock,
  Globe,
} from "lucide-react";

type Step = {
  icon: React.ComponentType<{ className?: string }>;
  title: string;
  body: string;
};

const STEPS: Step[] = [
  {
    icon: Sparkles,
    title: "Hello! 👋",
    body:
      "etcd-ui is a friendly window into any etcd cluster — Kubernetes, Patroni, or your own. Take a quick 30-second tour, or skip it any time.",
  },
  {
    icon: LayoutDashboard,
    title: "Dashboard",
    body:
      "The first page shows every cluster you have. A green dot means it's healthy. Click any card to dive in.",
  },
  {
    icon: FolderTree,
    title: "Keys = your data",
    body:
      "Open Keys to see everything stored in etcd, laid out like folders on a computer. Click a key to read it, edit it, or delete it.",
  },
  {
    icon: Radio,
    title: "Watch — live changes",
    body:
      "Curious what's changing right now? The Watch tab shows every PUT and DELETE in real-time. Like a security camera for your data.",
  },
  {
    icon: Workflow,
    title: "Transactions (advanced)",
    body:
      "Need to do many things at once — but only if some condition holds? Transactions guarantee that either everything happens, or nothing does.",
  },
  {
    icon: Users,
    title: "Users &amp; Roles",
    body: "Decide who can read or write. Add users, create roles, and grant fine-grained permissions.",
  },
  {
    icon: ScrollText,
    title: "Audit log",
    body: "Every change made through this UI is recorded. So if something looks off, you'll know who, what, and when.",
  },
  {
    icon: Wrench,
    title: "Maintenance",
    body:
      "Backups, defragment, alarms. Push a button — etcd-ui handles the rest. We never delete your data without confirming.",
  },
  {
    icon: Lock,
    title: "Distributed locks",
    body:
      "Acquire named locks under any prefix — same primitive clientv3/concurrency.Mutex uses internally. Open /locks in two tabs and watch fair queuing in real time.",
  },
  {
    icon: Globe,
    title: "Federation",
    body:
      "Connect multiple etcd-ui instances and aggregate all their clusters in one place. Each peer card shows the ACL rules governing its access at this hub; click 'manage' to filter the Permissions matrix to that peer.",
  },
  {
    icon: Command,
    title: "One last thing",
    body: "Hit ⌘K (or Ctrl+K) anywhere to jump to a page, switch clusters, or run a command. Have fun!",
  },
];

export function OnboardingTour() {
  const { tourCompleted, setTourCompleted } = useStore();
  const [i, setI] = useState(0);
  const [forceOpen, setForceOpen] = useState(false);

  // open automatically the first time
  useEffect(() => {
    const onShow = () => {
      setI(0);
      setForceOpen(true);
    };
    window.addEventListener("etcd-ui:show-tour", onShow);
    return () => window.removeEventListener("etcd-ui:show-tour", onShow);
  }, []);

  const open = forceOpen || !tourCompleted;
  const trapRef = useFocusTrap<HTMLDivElement>(open);
  if (!open) return null;

  const step = STEPS[i];
  const done = () => {
    setTourCompleted(true);
    setForceOpen(false);
  };

  return (
    <AnimatePresence>
      <motion.div
        key="overlay"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        className="fixed inset-0 z-50 bg-black/75 flex items-center justify-center p-6"
        role="dialog"
        aria-modal="true"
        aria-labelledby="onboarding-title"
      >
        <motion.div
          ref={trapRef}
          tabIndex={-1}
          key={i}
          initial={{ opacity: 0, y: 10, scale: 0.99 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={{ opacity: 0, y: -8 }}
          className="w-[560px] max-w-full panel p-7 shadow-elev"
        >
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-accent/15 flex items-center justify-center">
              <step.icon className="w-5 h-5 text-accent-500" />
            </div>
            <div>
              <div className="text-xs muted">
                Step {i + 1} of {STEPS.length}
              </div>
              <div id="onboarding-title" className="text-lg font-semibold">{step.title}</div>
            </div>
          </div>
          <p
            className="mt-4 text-[15px] leading-relaxed"
            dangerouslySetInnerHTML={{ __html: step.body }}
          />

          <div className="mt-6 flex items-center gap-3">
            <button onClick={done} className="btn btn-ghost muted">
              Skip tour
            </button>
            <div className="ml-auto flex items-center gap-2">
              {i > 0 && (
                <button onClick={() => setI(i - 1)} className="btn">
                  Back
                </button>
              )}
              {i < STEPS.length - 1 ? (
                <button onClick={() => setI(i + 1)} className="btn btn-primary">
                  Next
                </button>
              ) : (
                <button onClick={done} className="btn btn-primary">
                  Got it!
                </button>
              )}
            </div>
          </div>

          <div className="mt-4 flex gap-1.5 justify-center">
            {STEPS.map((_, j) => (
              <div
                key={j}
                className={"h-1.5 rounded-full transition-all " + (j === i ? "w-6 bg-accent-500" : "w-1.5 soft-active")}
              />
            ))}
          </div>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}
