import { Injectable, CanActivate, ExecutionContext } from '@nestjs/common';

@Injectable()
export class FakeJwtGuard implements CanActivate {
  canActivate(context: ExecutionContext): boolean {
    const req = context.switchToHttp().getRequest();
    const auth = req.headers['authorization'];
    if (!auth) return false;

    req.tenantId = req.headers['x-tenant-id'] || 'tenant_1';
    req.userId   = 'bench-user-1';
    req.role     = 'admin';
    return true;
  }
}
