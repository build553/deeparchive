import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { AuthProvider } from "../features/auth/AuthContext";
import ProtectedRoute from "./ProtectedRoute";
import Layout from "../components/Layout";
import LoginPage from "../features/auth/LoginPage";
import RegisterPage from "../features/auth/RegisterPage";
import ForgotPasswordPage from "../features/auth/ForgotPasswordPage";
import VerifyEmailPage from "../features/auth/VerifyEmailPage";
import ResetPasswordPage from "../features/auth/ResetPasswordPage";
import TermsPage from "../features/legal/TermsPage";
import PrivacyPage from "../features/legal/PrivacyPage";
import CookieNotice from "../components/CookieNotice";
import DevicesPage from "../features/devices/DevicesPage";
import BackupsPage from "../features/backups/BackupsPage";
import SnapshotDetailPage from "../features/backups/SnapshotDetailPage";
import AccountPage from "../features/account/AccountPage";
import DevicePage from "../features/device-auth/DevicePage";
import OrganizationsPage from "../features/organization/OrganizationsPage";
import OrganizationDetailPage from "../features/organization/OrganizationDetailPage";
import StoragePage from "../features/storage/StoragePage";
import DevInboxPage from "../features/devtools/DevInboxPage";
import AdminRoute from "../features/admin/AdminRoute";
import AdminLayout from "../features/admin/AdminLayout";
import AdminOverviewPage from "../features/admin/AdminOverviewPage";
import AdminUsersPage from "../features/admin/AdminUsersPage";
import AdminUserDetailPage from "../features/admin/AdminUserDetailPage";
import AdminOrganizationsPage from "../features/admin/AdminOrganizationsPage";
import AdminOrganizationDetailPage from "../features/admin/AdminOrganizationDetailPage";
import AdminActivityPage from "../features/admin/AdminActivityPage";

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
          <Route path="/forgot-password" element={<ForgotPasswordPage />} />
          <Route path="/verify-email" element={<VerifyEmailPage />} />
          <Route path="/reset-password" element={<ResetPasswordPage />} />
          <Route path="/terms" element={<TermsPage />} />
          <Route path="/privacy" element={<PrivacyPage />} />
          <Route path="/dev/inbox" element={<DevInboxPage />} />
          <Route element={<ProtectedRoute />}>
            <Route element={<Layout />}>
              <Route index element={<DevicesPage />} />
              <Route path="backups" element={<BackupsPage />} />
              <Route path="backups/:snapshotId" element={<SnapshotDetailPage />} />
              <Route path="organizations" element={<OrganizationsPage />} />
              <Route path="organizations/:organizationId" element={<OrganizationDetailPage />} />
              <Route path="storage" element={<StoragePage />} />
              <Route path="security" element={<Navigate to="/account" replace />} />
              <Route path="account" element={<AccountPage />} />
              <Route path="device" element={<DevicePage />} />
              <Route element={<AdminRoute />}>
                <Route path="admin" element={<AdminLayout />}>
                  <Route index element={<AdminOverviewPage />} />
                  <Route path="users" element={<AdminUsersPage />} />
                  <Route path="users/:userId" element={<AdminUserDetailPage />} />
                  <Route path="organizations" element={<AdminOrganizationsPage />} />
                  <Route path="organizations/:orgId" element={<AdminOrganizationDetailPage />} />
                  <Route path="activity" element={<AdminActivityPage />} />
                </Route>
              </Route>
            </Route>
          </Route>
        </Routes>
        <CookieNotice />
      </AuthProvider>
    </BrowserRouter>
  );
}
