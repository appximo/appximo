import { Injectable, CanActivate, ExecutionContext, UnauthorizedException } from '@nestjs/common';
import * as jwt from 'jsonwebtoken';

// Same trust model as the Appitools engine (S46 fair-comparison hardening):
//   - HS256 pinned (alg confusion / "none" rejected by the allowlist)
//   - signature verified on EVERY request with the shared HMAC secret
//   - exp claim REQUIRED (Appitools rejects tokens without expiry)
//   - tenant taken from the verified tenant_id claim, never from a header
const SECRET = process.env.JWT_SECRET || '';

@Injectable()
export class JwtGuard implements CanActivate {
  canActivate(context: ExecutionContext): boolean {
    if (!SECRET) throw new UnauthorizedException();
    const req = context.switchToHttp().getRequest();
    const auth: string = req.headers['authorization'] || '';
    if (!auth.startsWith('Bearer ')) throw new UnauthorizedException();

    let payload: jwt.JwtPayload;
    try {
      payload = jwt.verify(auth.slice('Bearer '.length), SECRET, {
        algorithms: ['HS256'],
      }) as jwt.JwtPayload;
    } catch {
      throw new UnauthorizedException();
    }
    if (!payload.exp || !payload.tenant_id) throw new UnauthorizedException();

    req.tenantId = `tenant_${payload.tenant_id}`;
    req.userId   = payload.user_id || '';
    req.role     = payload.role || '';
    return true;
  }
}
