export interface EnvironmentItem {
  name: string;
  namespace: string;
  phase: string;
  url?: string;
  routingKey?: string;
  branch?: string;
  services?: string[];
  createdAt?: string;
}

export interface DevSessionItem {
  service: string;
  developer: string;
  branch: string;
  hostname: string;
  heartbeat: string;
  status: string;
}

export interface DoctorIssue {
  severity: "INFO" | "WARNING" | "CRITICAL";
  component: string;
  summary: string;
  remediation: string;
}

export interface DoctorReport {
  healthy: boolean;
  namespace: string;
  environment_name?: string;
  issues: DoctorIssue[];
}
